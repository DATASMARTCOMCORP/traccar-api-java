package ical

import (
	"errors"
	"fmt"
	"time"
)

// kind is the java.time class ical4j parses an iCalendar date into.
type kind uint8

const (
	kindDate     kind = iota // LocalDate: VALUE=DATE values
	kindFloating             // LocalDateTime: date-times without a zone
	kindOffset               // OffsetDateTime: date-times with a UTC designator
	kindZoned                // ZonedDateTime: date-times with a TZID
)

// errUnsupported is java.time's UnsupportedTemporalTypeException (and ical4j's "Invalid
// temporal type for this rule"): Java throws, so the calendar check fails.
var errUnsupported = errors.New("unsupported temporal field or unit")

// errInvalidDate is DateTimeException for a field value that doesn't exist (Feb 30).
var errInvalidDate = errors.New("invalid date")

// temporal is a java.time value: a local date-time (held in a UTC time.Time; midnight for
// dates), plus the offset and zone for offset and zoned values.
type temporal struct {
	kind   kind
	local  time.Time
	offset int // seconds east of UTC
	zone   *zone
}

// zone is a ZoneId: its id and rules, either the JDK's (from Go's zone database) or,
// for TZID values, ical4j's own (see zonerules.go).
type zone struct {
	id     string
	loc    *time.Location
	ical4j *zoneRules
}

func (t temporal) hasTime() bool { return t.kind != kindDate }

// instant returns the instant of an offset or zoned value.
func (t temporal) instant() time.Time {
	return t.local.Add(-time.Duration(t.offset) * time.Second)
}

func newLocal(year int, month time.Month, day, hour, minute, second, nanos int) time.Time {
	return time.Date(year, month, day, hour, minute, second, nanos, time.UTC)
}

// offsetAt is the zone's offset at an instant.
func (z *zone) offsetAt(t time.Time) int {
	if z.ical4j != nil {
		return z.ical4j.offsetAt(t)
	}
	_, off := t.In(z.loc).Zone()
	return off
}

// validOffsets is ZoneRules.getValidOffsets for a local date-time: one offset normally,
// two in an overlap (the one before the transition first), none in a gap; before and
// after are the offsets around a gap.
func (z *zone) validOffsets(local time.Time) (offsets []int, before, after int) {
	if z.ical4j != nil {
		off, tr := z.ical4j.offsetInfo(local)
		switch {
		case tr == nil:
			return []int{off}, off, off
		case tr.isGap():
			return nil, tr.before, tr.after
		}
		return []int{tr.before, tr.after}, tr.before, tr.after
	}
	before = z.offsetAt(local.Add(-30 * time.Hour))
	after = z.offsetAt(local.Add(30 * time.Hour))
	for _, off := range []int{before, after} {
		if z.offsetAt(local.Add(-time.Duration(off)*time.Second)) == off && (len(offsets) == 0 || offsets[0] != off) {
			offsets = append(offsets, off)
		}
	}
	return offsets, before, after
}

// ofLocal is ZonedDateTime.ofLocal: the preferred offset wins in an overlap (otherwise
// the earlier one), and a gap moves the time forward by its length.
func ofLocal(local time.Time, z *zone, preferred *int) temporal {
	offsets, before, after := z.validOffsets(local)
	switch len(offsets) {
	case 0:
		return temporal{kind: kindZoned, local: local.Add(time.Duration(after-before) * time.Second), offset: after, zone: z}
	case 1:
		return temporal{kind: kindZoned, local: local, offset: offsets[0], zone: z}
	}
	if preferred != nil && (offsets[0] == *preferred || offsets[1] == *preferred) {
		return temporal{kind: kindZoned, local: local, offset: *preferred, zone: z}
	}
	return temporal{kind: kindZoned, local: local, offset: offsets[0], zone: z}
}

// ofInstant is ZonedDateTime.ofInstant.
func ofInstant(instant time.Time, z *zone) temporal {
	off := z.offsetAt(instant)
	return temporal{kind: kindZoned, local: instant.Add(time.Duration(off) * time.Second).UTC(), offset: off, zone: z}
}

// withLocal replaces the local date-time, resolving zoned values with the current offset
// preferred (ZonedDateTime.resolveLocal).
func (t temporal) withLocal(local time.Time) temporal {
	if t.kind == kindZoned {
		off := t.offset
		return ofLocal(local, t.zone, &off)
	}
	t.local = local
	return t
}

// Units of temporal arithmetic.
type unit uint8

const (
	seconds unit = iota
	minutes
	hours
	days
	weeks
	months
	years
	weekBasedYears // Recur's YEARLY increment with BYWEEKNO
)

// plus is Temporal.plus(amount, unit).
func (t temporal) plus(n int64, u unit, wf weekFields) (temporal, error) {
	switch u {
	case seconds, minutes, hours:
		if t.kind == kindDate {
			return t, errUnsupported
		}
		d := time.Duration(n) * map[unit]time.Duration{seconds: time.Second, minutes: time.Minute, hours: time.Hour}[u]
		if t.kind == kindZoned {
			return ofInstant(t.instant().Add(d), t.zone), nil
		}
		t.local = t.local.Add(d)
		return t, nil
	case days:
		return t.withLocal(t.local.AddDate(0, 0, int(n))), nil
	case weeks:
		return t.withLocal(t.local.AddDate(0, 0, int(7*n))), nil
	case months:
		return t.withLocal(plusMonths(t.local, n)), nil
	case years:
		return t.withLocal(plusMonths(t.local, 12*n)), nil
	case weekBasedYears:
		return t.plusWeekBasedYears(n, wf)
	}
	return t, errUnsupported
}

// plusMonths is LocalDate.plusMonths: the day is clamped to the new month's length.
func plusMonths(local time.Time, n int64) time.Time {
	total := int64(local.Year())*12 + int64(local.Month()-1) + n
	year, month := int(floorDiv(total, 12)), time.Month(floorMod(total, 12)+1)
	day := min(local.Day(), daysIn(year, month))
	return newLocal(year, month, day, local.Hour(), local.Minute(), local.Second(), local.Nanosecond())
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 { return a - floorDiv(a, b)*b }

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func daysInYear(year int) int {
	if isLeap(year) {
		return 366
	}
	return 365
}

func isLeap(year int) bool { return year%4 == 0 && (year%100 != 0 || year%400 == 0) }

// amount is a TemporalAmount: a java.time.Duration (seconds and nanos) or a
// java.time.Period (years, months, days).
type amount struct {
	period              bool
	secs                int64
	nanos               int64
	years, months, dayN int64
}

func (a amount) negated() amount {
	return amount{period: a.period, secs: -a.secs, nanos: -a.nanos, years: -a.years, months: -a.months, dayN: -a.dayN}
}

func (a amount) isZero() bool {
	return a.secs == 0 && a.nanos == 0 && a.years == 0 && a.months == 0 && a.dayN == 0
}

// add is Duration.addTo / Period.addTo.
func (t temporal) add(a amount, wf weekFields) (temporal, error) {
	var err error
	if !a.period {
		// Duration.addTo: seconds, then nanos.
		if a.secs != 0 {
			if t, err = t.plusNanos(a.secs * 1e9); err != nil {
				return t, err
			}
		}
		if a.nanos != 0 {
			if t, err = t.plusNanos(a.nanos); err != nil {
				return t, err
			}
		}
		return t, nil
	}
	if a.months == 0 && a.years != 0 {
		if t, err = t.plus(a.years, years, wf); err != nil {
			return t, err
		}
	} else if total := a.years*12 + a.months; total != 0 {
		if t, err = t.plus(total, months, wf); err != nil {
			return t, err
		}
	}
	if a.dayN != 0 {
		t, err = t.plus(a.dayN, days, wf)
	}
	return t, err
}

func (t temporal) plusNanos(n int64) (temporal, error) {
	if t.kind == kindDate {
		return t, errUnsupported
	}
	if t.kind == kindZoned {
		return ofInstant(t.instant().Add(time.Duration(n)), t.zone), nil
	}
	t.local = t.local.Add(time.Duration(n))
	return t, nil
}

// Fields read and set by the recurrence rules.
type field uint8

const (
	secondOfMinute field = iota
	minuteOfHour
	hourOfDay
	dayOfWeek // ISO, Monday = 1
	dayOfMonth
	dayOfYear
	monthOfYear
	yearField
)

// get is Temporal.get(field); time fields don't exist on dates.
func (t temporal) get(f field) (int, error) {
	l := t.local
	switch f {
	case secondOfMinute, minuteOfHour, hourOfDay:
		if t.kind == kindDate {
			return 0, errUnsupported
		}
		return map[field]int{secondOfMinute: l.Second(), minuteOfHour: l.Minute(), hourOfDay: l.Hour()}[f], nil
	case dayOfWeek:
		return isoWeekday(l.Weekday()), nil
	case dayOfMonth:
		return l.Day(), nil
	case dayOfYear:
		return l.YearDay(), nil
	case monthOfYear:
		return int(l.Month()), nil
	case yearField:
		return l.Year(), nil
	}
	return 0, errUnsupported
}

func isoWeekday(d time.Weekday) int {
	if d == time.Sunday {
		return 7
	}
	return int(d)
}

// with is Temporal.with(field, value).
func (t temporal) with(f field, v int) (temporal, error) {
	l := t.local
	switch f {
	case secondOfMinute, minuteOfHour, hourOfDay:
		if t.kind == kindDate {
			return t, errUnsupported
		}
		limit := map[field]int{secondOfMinute: 59, minuteOfHour: 59, hourOfDay: 23}[f]
		if v < 0 || v > limit {
			return t, fmt.Errorf("%w: field value %d", errInvalidDate, v)
		}
		h, m, s := l.Hour(), l.Minute(), l.Second()
		switch f {
		case secondOfMinute:
			s = v
		case minuteOfHour:
			m = v
		default:
			h = v
		}
		return t.withLocal(newLocal(l.Year(), l.Month(), l.Day(), h, m, s, l.Nanosecond())), nil
	case dayOfMonth:
		if v < 1 || v > daysIn(l.Year(), l.Month()) {
			return t, fmt.Errorf("%w: day %d", errInvalidDate, v)
		}
		return t.withLocal(newLocal(l.Year(), l.Month(), v, l.Hour(), l.Minute(), l.Second(), l.Nanosecond())), nil
	case dayOfYear:
		if v < 1 || v > daysInYear(l.Year()) {
			return t, fmt.Errorf("%w: day of year %d", errInvalidDate, v)
		}
		return t.withLocal(newLocal(l.Year(), 1, v, l.Hour(), l.Minute(), l.Second(), l.Nanosecond())), nil
	case monthOfYear:
		if v < 1 || v > 12 {
			return t, fmt.Errorf("%w: month %d", errInvalidDate, v)
		}
		day := min(l.Day(), daysIn(l.Year(), time.Month(v)))
		return t.withLocal(newLocal(l.Year(), time.Month(v), day, l.Hour(), l.Minute(), l.Second(), l.Nanosecond())), nil
	}
	return t, errUnsupported
}

// withDate replaces the date, keeping the time (Temporal.with(LocalDate)).
func (t temporal) withDate(year int, month time.Month, day int) temporal {
	l := t.local
	return t.withLocal(newLocal(year, month, day, l.Hour(), l.Minute(), l.Second(), l.Nanosecond()))
}

// equals is Java's equals for the value classes: same class and same fields (a zoned
// value also needs the same zone).
func (t temporal) equals(o temporal) bool {
	if t.kind != o.kind || !t.local.Equal(o.local) {
		return false
	}
	switch t.kind {
	case kindOffset:
		return t.offset == o.offset
	case kindZoned:
		return t.offset == o.offset && t.zone.id == o.zone.id
	}
	return true
}

// temporalKey identifies a value for equals (a map key).
type temporalKey struct {
	kind   kind
	local  int64
	offset int
	zone   string
}

func (t temporal) key() temporalKey {
	k := temporalKey{kind: t.kind, local: t.local.UnixNano()}
	switch t.kind {
	case kindOffset:
		k.offset = t.offset
	case kindZoned:
		k.offset, k.zone = t.offset, t.zone.id
	}
	return k
}

// comparator is ical4j's TemporalComparator with the default (JVM) zone.
type comparator struct {
	defaultZone *zone
}

// compare is TemporalComparator.compare. Same-class comparisons are exact for dates,
// local and (against a date) zoned values; other combinations compare the whole seconds
// between them (SECONDS.between), which can fail where java.time can't convert one
// value into the other's class.
func (c comparator) compare(a, b temporal) (int, error) {
	switch {
	case a.kind == kindDate && b.kind == kindDate,
		a.kind == kindFloating && b.kind == kindFloating:
		return a.local.Compare(b.local), nil
	case a.kind == kindFloating && b.kind == kindDate:
		return a.local.Compare(b.local), nil
	case a.kind == kindDate && b.kind == kindFloating:
		return a.local.Compare(b.local), nil
	case a.kind == kindZoned && b.kind == kindDate:
		return compareZoned(a, c.startOfDay(b)), nil
	case a.kind == kindDate && b.kind == kindZoned:
		return compareZoned(c.startOfDay(a), b), nil
	case a.kind == kindOffset && b.kind == kindFloating:
		return compareOffset(a, c.offsetFor(b)), nil
	case a.kind == kindFloating && b.kind == kindOffset:
		return compareOffset(c.offsetFor(a), b), nil
	case a.kind == kindOffset && b.kind == kindDate:
		return compareOffset(a, c.offsetFor(temporal{kind: kindFloating, local: b.local})), nil
	case a.kind == kindDate && b.kind == kindOffset:
		return compareOffset(c.offsetFor(temporal{kind: kindFloating, local: a.local}), b), nil
	}
	secs, err := secondsBetween(b, a)
	if err != nil {
		return 0, err
	}
	switch {
	case secs > 0:
		return 1, nil
	case secs < 0:
		return -1, nil
	}
	return 0, nil
}

// startOfDay is date.atStartOfDay().atZone(defaultZone).
func (c comparator) startOfDay(d temporal) temporal { return ofLocal(d.local, c.defaultZone, nil) }

// offsetFor is OffsetDateTime.of(local, defaultZone.getRules().getOffset(local)): the
// offset before a transition in gaps and overlaps alike.
func (c comparator) offsetFor(l temporal) temporal {
	offsets, before, _ := c.defaultZone.validOffsets(l.local)
	off := before
	if len(offsets) == 1 {
		off = offsets[0]
	}
	return temporal{kind: kindOffset, local: l.local, offset: off}
}

// compareZoned is ChronoZonedDateTime.compareTo: instant, then local date-time, then zone.
func compareZoned(a, b temporal) int {
	if c := a.instant().Compare(b.instant()); c != 0 {
		return c
	}
	if c := a.local.Compare(b.local); c != 0 {
		return c
	}
	switch {
	case a.zone.id < b.zone.id:
		return -1
	case a.zone.id > b.zone.id:
		return 1
	}
	return 0
}

// compareOffset is OffsetDateTime.compareTo: instant, then local date-time.
func compareOffset(a, b temporal) int {
	if c := a.instant().Compare(b.instant()); c != 0 {
		return c
	}
	return a.local.Compare(b.local)
}

// secondsBetween is ChronoUnit.SECONDS.between(from, to) = from.until(to, SECONDS): the
// whole seconds, truncated toward zero, after converting to to from's class.
func secondsBetween(from, to temporal) (int64, error) {
	var d time.Duration
	switch from.kind {
	case kindDate:
		return 0, errUnsupported
	case kindFloating:
		// LocalDateTime.from(to): the local part of any date-time.
		if to.kind == kindDate {
			return 0, errUnsupported
		}
		d = to.local.Sub(from.local)
	case kindOffset, kindZoned:
		if to.kind != kindOffset && to.kind != kindZoned {
			return 0, errUnsupported
		}
		d = to.instant().Sub(from.instant())
	}
	return int64(d / time.Second), nil
}
