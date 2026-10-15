package ingest

import (
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

func at(s string) model.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return model.NewTime(t)
}

// appendPosition keeps CacheManager's rules: older fixes are dropped, an equal fix
// time replaces the latest unless it was received earlier.
func TestAppendPosition(t *testing.T) {
	p := func(id int64, fix, server string) *model.Position {
		return &model.Position{ID: id, FixTime: at(fix), ServerTime: at(server)}
	}
	var list []*model.Position
	list = appendPosition(list, p(1, "2026-10-10T08:00:00Z", "2026-10-10T08:00:01Z"))
	list = appendPosition(list, p(2, "2026-10-10T07:59:00Z", "2026-10-10T08:00:02Z")) // older: dropped
	list = appendPosition(list, p(3, "2026-10-10T08:00:00Z", "2026-10-10T08:00:00Z")) // same fix, received earlier: dropped
	list = appendPosition(list, p(4, "2026-10-10T08:00:00Z", "2026-10-10T08:00:05Z")) // same fix, later: replaces
	list = appendPosition(list, p(5, "2026-10-10T08:01:00Z", "2026-10-10T08:01:00Z"))
	var ids []int64
	for _, q := range list {
		ids = append(ids, q.ID)
	}
	if !slices.Equal(ids, []int64{4, 5}) {
		t.Errorf("ids = %v, want [4 5]", ids)
	}
}

// adjustRollover moves pre-rollover GPS times forward by 1024 weeks.
func TestAdjustRollover(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	old := at("2007-03-02T10:00:00Z") // 1024 weeks before 2026-10-17
	got := adjustRollover(now, old)
	if want := old.Time.Add(rolloverCycle); !got.Time.Equal(want) {
		t.Errorf("got %v, want %v", got.Time, want)
	}
	recent := at("2026-10-01T10:00:00Z")
	if got := adjustRollover(now, recent); !got.Time.Equal(recent.Time) {
		t.Errorf("recent time changed: %v", got.Time)
	}
	epoch := model.NewTime(gpsEpoch)
	if got := adjustRollover(now, epoch); !got.Time.Equal(gpsEpoch) {
		t.Errorf("GPS epoch changed: %v", got.Time)
	}
}

// compareHolders orders buffered positions by fix, device, then server time; a missing
// time compares equal.
func TestCompareHolders(t *testing.T) {
	a := &model.Position{FixTime: at("2026-10-10T08:00:00Z"), DeviceTime: at("2026-10-10T08:00:00Z"), ServerTime: at("2026-10-10T08:00:01Z")}
	b := &model.Position{FixTime: at("2026-10-10T08:00:00Z"), DeviceTime: at("2026-10-10T08:00:00Z"), ServerTime: at("2026-10-10T08:00:02Z")}
	c := &model.Position{FixTime: at("2026-10-10T07:00:00Z"), ServerTime: at("2026-10-10T09:00:00Z")}
	if compareHolders(a, b) >= 0 || compareHolders(b, a) <= 0 || compareHolders(c, a) >= 0 {
		t.Error("wrong order")
	}
	if compareHolders(a, a) != 0 {
		t.Error("a position equals itself")
	}
}

func TestJavaSplit(t *testing.T) {
	re := regexp.MustCompile(`[ ,]`)
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{"a", []string{"a"}},
		{"a,b c", []string{"a", "b", "c"}},
		{",a", []string{"", "a"}},
		{"a,,", []string{"a"}},
		{",", nil},
	} {
		if got := javaSplitRegex(c.in, re); !slices.Equal(got, c.want) {
			t.Errorf("split %q = %q, want %q", c.in, got, c.want)
		}
		if got := javaSplitString(c.in, ","); c.in != "a,b c" && !slices.Equal(got, c.want) {
			t.Errorf("split %q = %q, want %q", c.in, got, c.want)
		}
	}
}
