package kit

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
)

// DateBuilder is Traccar's DateBuilder: a lenient java.util.Calendar in a zone (UTC by
// default). Out-of-range fields roll over (month 13 is January of the next year, day 0
// the last day of the previous month), and wall times at DST changes resolve as the JVM
// resolves them.
type DateBuilder struct {
	loc                                        *time.Location
	year, month, day, hour, minute, second, ms int // month is 0-based, as in Calendar
}

// NewDateBuilder is new DateBuilder(timeZone): the epoch in that zone (nil is UTC).
func NewDateBuilder(zone *time.Location) *DateBuilder {
	return NewDateBuilderAt(time.UnixMilli(0), zone)
}

// NewDateBuilderAt is new DateBuilder(time, timeZone).
func NewDateBuilderAt(t time.Time, zone *time.Location) *DateBuilder {
	if zone == nil {
		zone = time.UTC
	}
	b := &DateBuilder{loc: zone}
	b.setFields(t)
	return b
}

func (b *DateBuilder) setFields(t time.Time) {
	l := t.In(b.loc)
	b.year, b.month, b.day = l.Year(), int(l.Month())-1, l.Day()
	b.hour, b.minute, b.second, b.ms = l.Hour(), l.Minute(), l.Second(), l.Nanosecond()/1e6
}

func (b *DateBuilder) SetYear(year int) *DateBuilder {
	if year < 100 {
		year += 2000
	}
	b.year = year
	return b
}

func (b *DateBuilder) SetMonth(month int) *DateBuilder { b.month = month - 1; return b }
func (b *DateBuilder) SetDay(day int) *DateBuilder     { b.day = day; return b }

func (b *DateBuilder) SetDate(year, month, day int) *DateBuilder {
	return b.SetYear(year).SetMonth(month).SetDay(day)
}

func (b *DateBuilder) SetDateReverse(day, month, year int) *DateBuilder {
	return b.SetDate(year, month, day)
}

// SetCurrentDate sets today's date in the builder's zone (now is the current time).
func (b *DateBuilder) SetCurrentDate(now time.Time) *DateBuilder {
	l := now.In(b.loc)
	return b.SetYear(l.Year()).SetMonth(int(l.Month())).SetDay(l.Day())
}

func (b *DateBuilder) SetHour(v int) *DateBuilder   { b.hour = v; return b }
func (b *DateBuilder) SetMinute(v int) *DateBuilder { b.minute = v; return b }
func (b *DateBuilder) SetSecond(v int) *DateBuilder { b.second = v; return b }
func (b *DateBuilder) SetMillis(v int) *DateBuilder { b.ms = v; return b }

func (b *DateBuilder) SetTime(hour, minute, second int) *DateBuilder {
	return b.SetHour(hour).SetMinute(minute).SetSecond(second)
}

func (b *DateBuilder) SetTimeReverse(second, minute, hour int) *DateBuilder {
	return b.SetTime(hour, minute, second)
}

func (b *DateBuilder) SetTimeMillis(hour, minute, second, ms int) *DateBuilder {
	return b.SetTime(hour, minute, second).SetMillis(ms)
}

// AddMinute is Calendar.add(MINUTE): on the wall clock.
func (b *DateBuilder) AddMinute(n int) *DateBuilder { b.minute += n; b.setFields(b.Date()); return b }

// AddSeconds and AddMillis move the instant.
func (b *DateBuilder) AddSeconds(n int64) *DateBuilder {
	b.setFields(b.Date().Add(time.Duration(n) * time.Second))
	return b
}

func (b *DateBuilder) AddMillis(n int64) *DateBuilder {
	b.setFields(b.Date().Add(time.Duration(n) * time.Millisecond))
	return b
}

// Date is getDate().
func (b *DateBuilder) Date() time.Time {
	wall := time.Date(b.year, time.Month(b.month+1), b.day, b.hour, b.minute, b.second, 0, time.UTC).
		Add(time.Duration(b.ms) * time.Millisecond)
	if b.loc == time.UTC {
		return wall
	}
	return javatime.FromWall(wall, b.loc)
}
