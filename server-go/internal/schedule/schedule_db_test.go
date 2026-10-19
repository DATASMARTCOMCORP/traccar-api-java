package schedule

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/notify"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestTasksOnDatabase runs the tasks once against a Traccar database (SCHEDULE_TEST_DB, a
// JDBC URL of a scratch database such as a harness side, user postgres and password
// compat; it writes rows) with mail
// in debug mode, and checks what they leave behind.
func TestTasksOnDatabase(t *testing.T) {
	dbURL := os.Getenv("SCHEDULE_TEST_DB")
	if dbURL == "" {
		t.Skip("SCHEDULE_TEST_DB not set")
	}
	ctx := context.Background()
	cfg, err := config.Parse(strings.NewReader(fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8'?>
<!DOCTYPE properties SYSTEM 'http://java.sun.com/dtd/properties.dtd'>
<properties>
<entry key='database.driver'>org.postgresql.Driver</entry>
<entry key='database.url'>%s</entry>
<entry key='database.user'>postgres</entry>
<entry key='database.password'>compat</entry>
<entry key='mail.debug'>true</entry>
<entry key='notification.expiration.user'>true</entry>
<entry key='templates.root'>../../../templates</entry>
</properties>`, dbURL)), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	opts, err := store.OptionsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	n, err := notify.NewManager(cfg, st, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Minute)
	s := New(cfg, st, n, log)
	s.now = func() time.Time { return now }
	tag := fmt.Sprint(time.Now().UnixNano())

	user := model.User{Name: "sched-" + tag, Email: "sched-" + tag + "@example.com",
		ExpirationTime: model.NewTime(now.Add(-10 * time.Minute))}
	temp := model.User{Name: "temp-" + tag, Email: "temp-" + tag + "@example.com", Temporary: true,
		ExpirationTime: model.NewTime(now.Add(-time.Minute))}
	device := model.Device{Name: "sched-" + tag, UniqueID: "sched-" + tag, LastUpdate: model.NewTime(now.Add(-61 * time.Minute))}
	device.Attributes.Set("deviceInactivityStart", int64(3600000))
	for _, err := range []error{store.Insert(ctx, st, &user), store.Insert(ctx, st, &temp), store.Insert(ctx, st, &device)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	offline := model.StatusOffline
	device.Status = &offline // lastUpdate is written with the status, as ingest does
	if err := st.UpdateDeviceStatus(ctx, &device); err != nil {
		t.Fatal(err)
	}
	// An occurrence from two hours ago to five minutes ago: it ended in the last check.
	f := func(t time.Time) string { return t.UTC().Format("20060102T150405Z") }
	cal := model.Calendar{Name: "sched-" + tag, Data: []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:" + tag +
		"\r\nDTSTART:" + f(now.Add(-2*time.Hour)) + "\r\nDTEND:" + f(now.Add(-5*time.Minute)) + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")}
	if err := store.Insert(ctx, st, &cal); err != nil {
		t.Fatal(err)
	}
	report := model.Report{Type: "summary", CalendarID: cal.ID, Description: "sched"}
	report.Attributes.Set("daily", true)
	if err := store.Insert(ctx, st, &report); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		st.AddLink(ctx, store.KindUser, user.ID, store.KindReport, report.ID),
		st.AddLink(ctx, store.KindDevice, device.ID, store.KindReport, report.ID),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	for name, task := range map[string]func(context.Context) error{
		"reports": s.reports, "expirations": s.expirations, "inactivity": s.inactivity, "temporary": s.deleteTemporary,
	} {
		if err := task(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	actions, err := store.List[model.Action](ctx, st, store.ListOptions{Where: []store.Cond{store.Equals("userId", user.ID)}})
	if err != nil || len(actions) != 1 {
		t.Fatalf("actions: %v %v", actions, err)
	}
	if got, want := actions[0].Attributes.String(), fmt.Sprintf(`{"scheduled":true,"type":"summary","from":%q,"to":%q,"devices":"[%d]","groups":"[]"}`,
		now.Add(-2*time.Hour).Format("2006-01-02 15:04"), now.Add(-5*time.Minute).Format("2006-01-02 15:04"), device.ID); got != want {
		t.Errorf("action: %s, want %s", got, want)
	}
	events, err := store.List[model.Event](ctx, st, store.ListOptions{Where: []store.Cond{store.Equals("deviceId", device.ID)}})
	if err != nil || len(events) != 1 || events[0].Type != model.EventDeviceInactive {
		t.Errorf("events: %v %v", events, err)
	}
	if _, err := store.Get[model.User](ctx, st, temp.ID); err == nil {
		t.Error("the expired temporary user is still there")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), "Scheduled report is ready") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	out := logs.String()
	for _, want := range []string{"Subject: Account expiration", "Subject: Report", "/reports/summary?deviceId=" + fmt.Sprint(device.ID) + "&daily=true&from="} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in the logs:\n%s", want, out)
		}
	}
}
