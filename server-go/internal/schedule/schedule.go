// Package schedule runs Java's ScheduleManager tasks that the rest of the server doesn't:
// deleting expired temporary users, device inactivity events, expiration mails,
// scheduled reports and the systemd watchdog.
//
// The others live with what they act on: the status reset at startup (cmd), the idle
// session sweep (ingest) and the socket keepalive (live).
package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ical"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/notify"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

const (
	hourly        = time.Hour        // TaskDeleteTemporary, TaskExpirations
	quarterHourly = 15 * time.Minute // TaskDeviceInactivityCheck, TaskReports
)

// Scheduler holds what the tasks use.
type Scheduler struct {
	st     *store.Store
	cfg    *config.Config
	notify *notify.Manager
	log    *slog.Logger
	now    func() time.Time
}

// New makes a scheduler; notify sends the events and mails.
func New(cfg *config.Config, st *store.Store, n *notify.Manager, log *slog.Logger) *Scheduler {
	return &Scheduler{st: st, cfg: cfg, notify: n, log: log, now: time.Now}
}

// Run schedules the tasks at Java's fixed rates (the first run one period after start)
// until ctx ends. A secondary server (broadcast.secondary) runs none of them, as they are
// all single-instance tasks.
func (s *Scheduler) Run(ctx context.Context) {
	s.watchdog(ctx)
	if s.cfg.Bool("broadcast.secondary", false) {
		return
	}
	every := func(period time.Duration, name string, task func(context.Context) error) {
		go func() {
			t := time.NewTicker(period)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if err := task(ctx); err != nil {
						s.log.Warn(name+" failed", "err", err)
					}
				}
			}
		}()
	}
	every(hourly, "temporary users cleanup", s.deleteTemporary)
	every(hourly, "expiration check", s.expirations)
	every(quarterHourly, "scheduled reports", s.reports)
	every(quarterHourly, "device inactivity check", s.inactivity)
}

// deleteTemporary is TaskDeleteTemporary.
func (s *Scheduler) deleteTemporary(ctx context.Context) error {
	return s.st.DeleteExpiredTemporaryUsers(ctx, model.NewTime(s.now()))
}

// inactivity is TaskDeviceInactivityCheck: a deviceInactive event for each device whose
// inactivity start (or a repeat of the inactivity period) fell in the last check period.
// Java's task dies on an unexpected exception and checks nothing until a restart; this
// one logs and tries again next period.
func (s *Scheduler) inactivity(ctx context.Context) error {
	now := s.now().UnixMilli()
	checkPeriod := quarterHourly.Milliseconds()
	groupList, err := store.All[model.Group](ctx, s.st)
	if err != nil {
		return err
	}
	groups := map[int64]*model.Group{}
	for i := range groupList {
		groups[groupList[i].ID] = &groupList[i]
	}
	devices, err := store.All[model.Device](ctx, s.st)
	if err != nil {
		return err
	}
	type pending struct {
		device model.Device
		event  model.Event
	}
	var events []pending
	for _, d := range devices {
		if d.LastUpdate.IsZero() {
			continue
		}
		due, err := inactive(&d, groups, now, checkPeriod)
		if err != nil {
			return err
		}
		if due {
			e := model.Event{Type: model.EventDeviceInactive, DeviceID: d.ID, EventTime: model.NewTime(s.now())}
			e.Attributes.Set("lastUpdate", d.LastUpdate.Time.UnixMilli())
			events = append(events, pending{d, e})
		}
	}
	for _, p := range events {
		if err := store.Insert(ctx, s.st, &p.event); err != nil {
			s.log.Warn("Event save error", "err", err)
		}
		if err := s.notify.Event(ctx, &p.event, nil, p.device); err != nil {
			return err
		}
	}
	return nil
}

// attribute is TaskDeviceInactivityCheck.getAttribute: the device's positive value, else
// the first positive one up its group chain.
func attribute(d *model.Device, groups map[int64]*model.Group, key string) (int64, error) {
	v, err := d.Attributes.JavaLong(key)
	if err != nil || v > 0 {
		return v, err
	}
	for id := d.GroupID; id > 0; {
		g := groups[id]
		if g == nil {
			return 0, nil
		}
		v, err := g.Attributes.JavaLong(key)
		if err != nil || v > 0 {
			return v, err
		}
		id = g.GroupID
	}
	return 0, nil
}

// inactive is TaskDeviceInactivityCheck.checkDevice.
func inactive(d *model.Device, groups map[int64]*model.Group, now, checkPeriod int64) (bool, error) {
	start, err := attribute(d, groups, "deviceInactivityStart")
	if err != nil || start <= 0 {
		return false, err
	}
	threshold := d.LastUpdate.Time.UnixMilli() + start
	if now < threshold {
		return false, nil
	}
	if now-checkPeriod < threshold {
		return true, nil
	}
	period, err := attribute(d, groups, "deviceInactivityPeriod")
	if err != nil || period <= 0 {
		return false, err
	}
	count := (now - threshold - 1) / period
	threshold += count * period
	return now-checkPeriod < threshold, nil
}

// expired is TaskExpirations.checkTimeTrigger: the expiration (moved by offset) fell in
// the last hour.
func expired(expiration model.Time, now time.Time, offset int64) bool {
	if expiration.IsZero() {
		return false
	}
	current := now.UnixMilli()
	previous := current - hourly.Milliseconds()
	t := expiration.Time.UnixMilli() + offset
	return previous < t && current >= t
}

// expirations is TaskExpirations: mails to users whose account, or whose device, expired
// in the last hour, or will after the configured reminder. A failure stops the run.
func (s *Scheduler) expirations(ctx context.Context) error {
	now := s.now()
	servers, err := store.All[model.Server](ctx, s.st)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return errors.New("no server row")
	}
	server := &servers[0]

	if s.cfg.Bool("notification.expiration.user", false) {
		reminder, err := s.cfg.Int64("notification.expiration.user.reminder", 0)
		if err != nil {
			return err
		}
		users, err := store.All[model.User](ctx, s.st)
		if err != nil {
			return err
		}
		for i := range users {
			u := &users[i]
			x := notify.TextExtra{Expiration: u.ExpirationTime}
			switch {
			case expired(u.ExpirationTime, now, 0):
				err = s.notify.MailText(server, u, true, "userExpiration", x)
			case reminder > 0 && expired(u.ExpirationTime, now, -reminder):
				err = s.notify.MailText(server, u, true, "userExpirationReminder", x)
			}
			if err != nil {
				return err
			}
		}
	}

	if s.cfg.Bool("notification.expiration.device", false) {
		reminder, err := s.cfg.Int64("notification.expiration.device.reminder", 0)
		if err != nil {
			return err
		}
		devices, err := store.All[model.Device](ctx, s.st)
		if err != nil {
			return err
		}
		for i := range devices {
			d := &devices[i]
			template := ""
			switch {
			case expired(d.ExpirationTime, now, 0):
				template = "deviceExpiration"
			case reminder > 0 && expired(d.ExpirationTime, now, -reminder):
				template = "deviceExpirationReminder"
			default:
				continue
			}
			users, err := store.List[model.User](ctx, s.st, store.ListOptions{
				Where: []store.Cond{store.PermissionOf(store.KindUser, store.KindDevice, d.ID, false)}})
			if err != nil {
				return err
			}
			for j := range users {
				x := notify.TextExtra{Expiration: d.ExpirationTime, Device: d}
				if err := s.notify.MailText(server, &users[j], true, template, x); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// reports is TaskReports: for each scheduled report, the calendar periods that ended in
// the last check period are mailed, as a link to the web app's report, to the report's
// users. An error (a missing calendar included) stops the run, as Java's catch-all does.
func (s *Scheduler) reports(ctx context.Context) error {
	current := s.now()
	last := current.Add(-quarterHourly)
	reports, err := store.All[model.Report](ctx, s.st)
	if err != nil {
		return err
	}
	opts := ical.Options{DefaultZone: s.st.Location(), FirstDayOfWeek: time.Sunday}
	for _, r := range reports {
		c, err := store.Get[model.Calendar](ctx, s.st, r.CalendarID)
		if err != nil {
			return fmt.Errorf("report %d calendar: %w", r.ID, err)
		}
		cal, err := ical.Parse(c.Data)
		if err != nil {
			return err
		}
		lastPeriods, err := cal.FindPeriods(last, opts)
		if err != nil {
			return err
		}
		currentPeriods, err := cal.FindPeriods(current, opts)
		if err != nil {
			return err
		}
		for _, p := range lastPeriods {
			finished := true
			for _, q := range currentPeriods {
				if p.Start.Equal(q.Start) && p.End.Equal(q.End) {
					finished = false
				}
			}
			if finished {
				if err := s.report(ctx, r, p.Start, p.End); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// report is TaskReports.executeReport.
func (s *Scheduler) report(ctx context.Context, r model.Report, from, to time.Time) error {
	deviceIDs, err := store.IDs[model.Device](ctx, s.st, store.PermissionOf(store.KindDevice, store.KindReport, r.ID, false))
	if err != nil {
		return err
	}
	groupIDs, err := store.IDs[model.Group](ctx, s.st, store.PermissionOf(store.KindGroup, store.KindReport, r.ID, false))
	if err != nil {
		return err
	}
	users, err := store.List[model.User](ctx, s.st, store.ListOptions{
		Where: []store.Cond{store.PermissionOf(store.KindUser, store.KindReport, r.ID, false)}})
	if err != nil {
		return err
	}
	servers, err := store.All[model.Server](ctx, s.st)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return errors.New("no server row")
	}

	var b strings.Builder
	b.WriteString("/reports/" + r.Type + "?")
	for _, id := range deviceIDs {
		fmt.Fprintf(&b, "deviceId=%d&", id)
	}
	for _, id := range groupIDs {
		fmt.Fprintf(&b, "groupId=%d&", id)
	}
	if r.Attributes.Has("daily") {
		fmt.Fprintf(&b, "daily=%t&", r.Attributes.JavaBool("daily"))
	}
	b.WriteString("from=" + url.QueryEscape(javatime.FormatISOOffset(from.In(time.Local))))
	b.WriteString("&to=" + url.QueryEscape(javatime.FormatISOOffset(to.In(time.Local))))
	link := b.String()

	format := func(t time.Time) string { return t.In(time.Local).Format("2006-01-02 15:04") }
	for i := range users {
		u := &users[i]
		// LogAction.report without a request: no address.
		act := model.Action{ActionTime: model.NewTime(s.now()), UserID: u.ID, ActionType: "report"}
		act.Attributes.Set("scheduled", true)
		act.Attributes.Set("type", r.Type)
		act.Attributes.Set("from", format(from))
		act.Attributes.Set("to", format(to))
		act.Attributes.Set("devices", javaList(deviceIDs))
		act.Attributes.Set("groups", javaList(groupIDs))
		if err := store.Insert(ctx, s.st, &act); err != nil {
			s.log.Warn("failed to store action", "type", "report", "err", err)
		}
		// ReportMailer.sendAsync: failures are only logged.
		go func(u *model.User) {
			err := s.notify.MailText(&servers[0], u, false, "scheduledReport", notify.TextExtra{ReportURL: link})
			if err != nil {
				s.log.Warn("Email report failed", "err", err)
			}
		}(u)
	}
	return nil
}

// javaList is List.toString.
func javaList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// watchdog is TaskHealthCheck: under systemd with a watchdog (NOTIFY_SOCKET and
// WATCHDOG_USEC set, web.disableHealthCheck off), WATCHDOG=1 every four fifths of the
// watchdog period: always during the first hour, then only while /api/health answers 200.
func (s *Scheduler) watchdog(ctx context.Context) {
	socket := os.Getenv("NOTIFY_SOCKET")
	if s.cfg.Bool("web.disableHealthCheck", false) || socket == "" {
		return
	}
	usec, _ := strconv.ParseInt(os.Getenv("WATCHDOG_USEC"), 10, 64)
	period := time.Duration(usec/1000*4/5) * time.Millisecond
	if period <= 0 {
		return
	}
	s.log.Info(fmt.Sprintf("Health check enabled with period %d", period.Milliseconds()))
	grace := s.now().Add(time.Hour)
	port, _ := s.cfg.Int(config.WebPort, config.DefaultWebPort)
	health := "http://" + net.JoinHostPort(s.cfg.String(config.WebAddress, "localhost"), strconv.Itoa(port)) + "/api/health"
	if strings.HasPrefix(socket, "@") { // an abstract socket
		socket = "\x00" + socket[1:]
	}
	notifyWatchdog := func() {
		conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
		if err != nil {
			s.log.Warn("watchdog notify failed", "err", err)
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("WATCHDOG=1")); err != nil {
			s.log.Warn("watchdog notify failed", "err", err)
		}
	}
	go func() {
		t := time.NewTicker(period)
		defer t.Stop()
		client := &http.Client{Timeout: period}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if s.now().Before(grace) {
				notifyWatchdog()
				continue
			}
			resp, err := client.Get(health)
			if err != nil {
				s.log.Warn("health check failed", "err", err)
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				notifyWatchdog()
			}
		}
	}()
}
