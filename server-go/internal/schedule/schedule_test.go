package schedule

import (
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

func TestInactive(t *testing.T) {
	const minute = int64(60000)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	device := func(lastUpdate time.Time, start, period int64, group int64) *model.Device {
		d := &model.Device{LastUpdate: model.NewTime(lastUpdate), GroupID: group}
		if start != 0 {
			d.Attributes.Set("deviceInactivityStart", start)
		}
		if period != 0 {
			d.Attributes.Set("deviceInactivityPeriod", period)
		}
		return d
	}
	parent := &model.Group{ID: 1}
	parent.Attributes.Set("deviceInactivityStart", 60*minute)
	child := &model.Group{ID: 2, GroupID: 1}
	groups := map[int64]*model.Group{1: parent, 2: child}
	for _, c := range []struct {
		name string
		d    *model.Device
		want bool
	}{
		{"no start", device(now.Add(-time.Hour), 0, 0, 0), false},
		{"not yet", device(now.Add(-59*time.Minute), 60*minute, 0, 0), false},
		{"just reached", device(now.Add(-60*time.Minute), 60*minute, 0, 0), true},
		{"reached in the period", device(now.Add(-74*time.Minute), 60*minute, 0, 0), true},
		{"reached before the period", device(now.Add(-75*time.Minute), 60*minute, 0, 0), false},
		{"repeat in the period", device(now.Add(-125*time.Minute), 60*minute, 60*minute, 0), true},
		{"repeat not due", device(now.Add(-100*time.Minute), 60*minute, 60*minute, 0), false},
		{"start from the group chain", device(now.Add(-61*time.Minute), 0, 0, 2), true},
		{"device value first", device(now.Add(-61*time.Minute), 120*minute, 0, 2), false},
	} {
		got, err := inactive(c.d, groups, now.UnixMilli(), 15*minute)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v (%v), want %v", c.name, got, err, c.want)
		}
	}
	bad := device(now.Add(-time.Hour), 0, 0, 0)
	bad.Attributes.Set("deviceInactivityStart", "soon")
	if _, err := inactive(bad, groups, now.UnixMilli(), 15*minute); err == nil {
		t.Error("a non-numeric start: no error")
	}
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) model.Time { return model.NewTime(now.Add(d)) }
	day := int64(24 * time.Hour / time.Millisecond)
	for _, c := range []struct {
		name       string
		expiration model.Time
		offset     int64
		want       bool
	}{
		{"none", model.Time{}, 0, false},
		{"now", at(0), 0, true},
		{"in the last hour", at(-59 * time.Minute), 0, true},
		{"an hour ago", at(-time.Hour), 0, false},
		{"in the future", at(time.Minute), 0, false},
		{"reminder a day ahead", at(24*time.Hour - 30*time.Minute), -day, true},
		{"reminder not yet", at(25 * time.Hour), -day, false},
	} {
		if got := expired(c.expiration, now, c.offset); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
