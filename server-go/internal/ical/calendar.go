// Package ical decides whether a moment falls within an iCalendar calendar, as
// Traccar's Calendar.checkMoment does with ical4j 4.2.5. Calendars schedule geofences,
// notifications and the position filter of devices, so Go must answer exactly as Java:
// this is a port of the ical4j code paths involved (date parsing, the recurrence set of
// each VEVENT, RRULE expansion), including their quirks, such as the moment-at-start
// exclusion of non-recurring events and the inclusive ends of recurring ones.
//
// Not covered, and reported as errors: VTIMEZONE definitions that differ from the zone
// database (Go uses the TZID's IANA zone), SKIP/RSCALE and leap months.
package ical

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
)

// Options are the JVM defaults ical4j depends on.
type Options struct {
	// DefaultZone is the JVM's default zone (ical4j's TimeZones.getDefault()), used to
	// compare dates with date-times.
	DefaultZone *time.Location
	// FirstDayOfWeek is WeekFields.of(Locale.getDefault()).getFirstDayOfWeek(), which
	// weekly BYDAY expansion uses without WKST. Sunday for en_US and pt_BR.
	FirstDayOfWeek time.Weekday
}

func (o Options) defaultZone() *zone {
	loc := o.DefaultZone
	if loc == nil {
		loc = time.Local
	}
	return &zone{id: loc.String(), loc: loc}
}

func (o Options) localeWeek() weekFields {
	return weekFields{firstDay: isoWeekday(o.FirstDayOfWeek), minimalDay: 1}
}

// Calendar is a parsed iCalendar file.
type Calendar struct {
	events []*event
}

type property struct {
	name   string
	params map[string]string
	value  string
}

type event struct {
	props []property
}

func (e *event) all(name string) []property {
	var out []property
	for _, p := range e.props {
		if p.name == name {
			out = append(out, p)
		}
	}
	return out
}

func (e *event) first(name string) *property {
	for i := range e.props {
		if e.props[i].name == name {
			return &e.props[i]
		}
	}
	return nil
}

// Parse reads the VEVENT components of an iCalendar file. (Stored calendars were already
// accepted by ical4j when they were saved, so the parser is lenient.)
func Parse(data []byte) (*Calendar, error) {
	text := strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// Unfold: a line break followed by a space or tab continues the line.
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\n ", ""), "\n\t", "")
	cal := &Calendar{}
	var stack []string
	var current *event
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		switch p.name {
		case "BEGIN":
			name := strings.ToUpper(p.value)
			stack = append(stack, name)
			if name == "VEVENT" && len(stack) == 2 {
				current = &event{}
			}
			continue
		case "END":
			if len(stack) == 0 {
				return nil, errors.New("unexpected END")
			}
			if stack[len(stack)-1] == "VEVENT" && current != nil && len(stack) == 2 {
				cal.events = append(cal.events, current)
				current = nil
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if current != nil && len(stack) == 2 {
			current.props = append(current.props, p)
		}
	}
	if len(stack) != 0 {
		return nil, errors.New("unterminated component")
	}
	return cal, nil
}

// parseLine splits "NAME;PARAM=VALUE;PARAM=\"QUOTED\":value".
func parseLine(line string) (property, error) {
	p := property{params: map[string]string{}}
	i := strings.IndexAny(line, ";:")
	if i < 0 {
		return p, fmt.Errorf("invalid content line %q", line)
	}
	p.name = strings.ToUpper(line[:i])
	rest := line[i:]
	for strings.HasPrefix(rest, ";") {
		rest = rest[1:]
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			return p, fmt.Errorf("invalid parameter in %q", line)
		}
		name := strings.ToUpper(rest[:eq])
		rest = rest[eq+1:]
		var value string
		if strings.HasPrefix(rest, "\"") {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return p, fmt.Errorf("unterminated quote in %q", line)
			}
			value, rest = rest[1:end+1], rest[end+2:]
		} else {
			end := strings.IndexAny(rest, ";:")
			if end < 0 {
				return p, fmt.Errorf("invalid content line %q", line)
			}
			value, rest = rest[:end], rest[end:]
		}
		p.params[name] = value
	}
	if !strings.HasPrefix(rest, ":") {
		return p, fmt.Errorf("invalid content line %q", line)
	}
	p.value = rest[1:]
	return p, nil
}

// Contains is Calendar.checkMoment: whether any VEVENT has an occurrence at the moment.
// Java computes the occurrences of every VEVENT before answering, so an error in any of
// them is an error (where Java throws; the caller's handler then fails, as in Java).
func (c *Calendar) Contains(moment time.Time, opts Options) (bool, error) {
	cmp := comparator{defaultZone: opts.defaultZone()}
	found := false
	for _, e := range c.events {
		in, err := e.contains(moment, opts, cmp)
		if err != nil {
			return false, err
		}
		found = found || in
	}
	return found, nil
}

// zoneOf resolves a TZID as ical4j's global registry does: a zone id Java knows, with
// ical4j's rules for it.
func zoneOf(tzid string) (*zone, error) {
	rules, err := ical4jZone(tzid)
	if err != nil {
		return nil, err
	}
	return &zone{id: tzid, ical4j: rules}, nil
}

// javaZone is ZoneId.of(id) with the JDK's rules.
func javaZone(id string) (*zone, error) {
	if !javatime.IsZoneID(id) {
		return nil, fmt.Errorf("unknown time-zone ID: %s", id)
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, err
	}
	return &zone{id: id, loc: loc}, nil
}

// dateValue parses a DATE or DATE-TIME property value (DateProperty.setValue/getDate):
// with a TZID, "yyyyMMdd'T'HHmmss" in that zone; otherwise ical4j's default parse format,
// a date, a local date-time, or one with a UTC designator or offset.
func dateValue(p property) (temporal, error) {
	tzid, hasTZ := p.params["TZID"]
	if hasTZ {
		z, err := zoneOf(tzid)
		if err != nil {
			return temporal{}, err
		}
		local, ok := parseCompact(p.value, true)
		if !ok || len(p.value) != 15 {
			return temporal{}, fmt.Errorf("invalid date-time %q", p.value)
		}
		return ofLocal(local, z, nil), nil
	}
	return parseValue(p.value, nil)
}

// parseValue is CalendarDateFormat.DEFAULT_PARSE_FORMAT ("yyyyMMdd['T'HHmmss[X]]") with
// parseBest: an OffsetDateTime if there is an offset, else a LocalDateTime, else a
// LocalDate.
func parseValue(s string, _ *zone) (temporal, error) {
	switch {
	case len(s) == 8:
		local, ok := parseCompact(s, false)
		if !ok {
			return temporal{}, fmt.Errorf("invalid date %q", s)
		}
		return temporal{kind: kindDate, local: local}, nil
	case len(s) >= 15 && s[8] == 'T':
		local, ok := parseCompact(s[:15], true)
		if !ok {
			return temporal{}, fmt.Errorf("invalid date-time %q", s)
		}
		rest := s[15:]
		if rest == "" {
			return temporal{kind: kindFloating, local: local}, nil
		}
		off, ok := parseOffsetX(rest)
		if !ok {
			return temporal{}, fmt.Errorf("invalid date-time %q", s)
		}
		return temporal{kind: kindOffset, local: local, offset: off}, nil
	}
	return temporal{}, fmt.Errorf("invalid date %q", s)
}

// parseCompact reads yyyyMMdd and, with withTime, 'T'HHmmss, validating the fields
// strictly (ResolverStyle.SMART for these patterns rejects out-of-range values).
func parseCompact(s string, withTime bool) (time.Time, bool) {
	num := func(from, n int) (int, bool) {
		if from+n > len(s) {
			return 0, false
		}
		v := 0
		for i := from; i < from+n; i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
			v = 10*v + int(s[i]-'0')
		}
		return v, true
	}
	year, ok1 := num(0, 4)
	month, ok2 := num(4, 2)
	day, ok3 := num(6, 2)
	if !ok1 || !ok2 || !ok3 || month < 1 || month > 12 || day < 1 || day > 31 || year < 1 {
		return time.Time{}, false
	}
	// SMART resolution clamps days past the month's end.
	day = min(day, daysIn(year, time.Month(month)))
	if !withTime {
		return newLocal(year, time.Month(month), day, 0, 0, 0, 0), true
	}
	if len(s) < 15 || s[8] != 'T' {
		return time.Time{}, false
	}
	hour, ok1 := num(9, 2)
	minute, ok2 := num(11, 2)
	second, ok3 := num(13, 2)
	if !ok1 || !ok2 || !ok3 || hour > 24 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	if hour == 24 {
		if minute != 0 || second != 0 {
			return time.Time{}, false
		}
		return newLocal(year, time.Month(month), day+1, 0, 0, 0, 0), true
	}
	return newLocal(year, time.Month(month), day, hour, minute, second, 0), true
}

// parseOffsetX is the pattern letter X (appendOffset("+HHmm", "Z")): "Z", "+HH" or
// "+HHmm".
func parseOffsetX(s string) (int, bool) {
	if s == "Z" {
		return 0, true
	}
	if len(s) != 3 && len(s) != 5 || s[0] != '+' && s[0] != '-' {
		return 0, false
	}
	h, err := strconv.Atoi(s[1:3])
	if err != nil || h > 23 {
		return 0, false
	}
	m := 0
	if len(s) == 5 {
		if m, err = strconv.Atoi(s[3:5]); err != nil || m > 59 {
			return 0, false
		}
	}
	off := h*3600 + m*60
	if s[0] == '-' {
		off = -off
	}
	return off, true
}

// period is ical4j's Period: start, end (start plus duration) and the duration.
type period struct {
	start, end temporal
	duration   amount
	noDuration bool // new Period(start, end): no duration
}

func newPeriod(start temporal, d amount, wf weekFields) (period, error) {
	end, err := start.add(d, wf)
	return period{start: start, end: end, duration: d}, err
}

func (p period) equals(o period) bool {
	return p.start.equals(o.start) && p.end.equals(o.end) && p.noDuration == o.noDuration &&
		(p.noDuration || p.duration == o.duration)
}

// intersects is Period.intersects.
func (p period) intersects(o period, cmp comparator) (bool, error) {
	if o.equals(p) {
		return true, nil
	}
	c1, err := cmp.compare(p.start, o.end)
	if err != nil || c1 >= 0 {
		return false, err
	}
	c2, err := cmp.compare(o.start, p.end)
	return c2 < 0, err
}

// includes is Period.includes(date).
func (p period) includes(t temporal, cmp comparator) (bool, error) {
	c1, err := cmp.compare(p.start, t)
	if err != nil || c1 > 0 {
		return false, err
	}
	c2, err := cmp.compare(p.end, t)
	return c2 >= 0, err
}

// toLocalTime is TemporalAdapter.toLocalTime(default zone), for the implicit BYMONTHDAY.
func (c comparator) toLocalTime(t temporal) temporal {
	if t.kind == kindOffset {
		return ofInstant(t.instant(), c.defaultZone)
	}
	return t
}

// contains is checkMoment for one VEVENT: the moment, converted to the class of DTSTART,
// as a zero-length period, and Component.calculateRecurrenceSet with it.
func (e *event) contains(moment time.Time, opts Options, cmp comparator) (bool, error) {
	startProp := e.first("DTSTART")
	if startProp == nil {
		return false, errors.New("VEVENT without DTSTART")
	}
	start, err := dateValue(*startProp)
	if err != nil {
		return false, err
	}
	wf := opts.localeWeek()

	// Traccar's convertToMatchingTemporal and resolveOverrideZone.
	var q temporal
	switch start.kind {
	case kindDate:
		u := moment.UTC()
		q = temporal{kind: kindDate, local: newLocal(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0)}
	case kindFloating:
		q = temporal{kind: kindFloating, local: moment.UTC()}
	case kindOffset:
		q = temporal{kind: kindOffset, local: moment.UTC().Add(time.Duration(start.offset) * time.Second), offset: start.offset}
	case kindZoned:
		if tzid := startProp.params["TZID"]; javatime.IsZoneID(tzid) {
			override, err := javaZone(tzid)
			if err != nil {
				return false, err
			}
			local := ofInstant(moment, override).local
			q = ofLocal(local, start.zone, nil)
		} else {
			q = ofInstant(moment, start.zone)
		}
	}
	query := period{start: q, end: q}

	duration, err := e.duration(start)
	if err != nil {
		return false, err
	}

	// The recurrence set (a TreeSet in Java). Only whether it ends up empty matters, and
	// equal periods are removed together, so duplicates can stay.
	var set []period
	add := func(p period) { set = append(set, p) }
	for _, p := range e.all("RDATE") {
		if strings.EqualFold(p.params["VALUE"], "PERIOD") {
			periods, err := periodList(p)
			if err != nil {
				return false, err
			}
			for _, rp := range periods {
				ok, err := query.intersects(rp, cmp)
				if err != nil {
					return false, err
				}
				if ok {
					add(rp)
				}
			}
			continue
		}
		dates, err := dateList(p)
		if err != nil {
			return false, err
		}
		for _, d := range dates {
			ok, err := query.includes(d, cmp)
			if err != nil {
				return false, err
			}
			if ok {
				rp, err := newPeriod(d, duration, wf)
				if err != nil {
					return false, err
				}
				add(rp)
			}
		}
	}

	startMinus, err := q.add(duration.negated(), wf)
	if err != nil {
		return false, err
	}
	rrules := e.all("RRULE")
	if len(rrules) > 0 {
		for _, p := range rrules {
			r, err := parseRecur(p.value, opts)
			if err != nil {
				return false, err
			}
			dates, err := r.dates(start, startMinus, q, cmp)
			if err != nil {
				return false, err
			}
			for _, d := range dates {
				rp, err := newPeriod(d, duration, wf)
				if err != nil {
					return false, err
				}
				add(rp)
			}
		}
	} else {
		initial, err := newPeriod(start, duration, wf)
		if err != nil {
			return false, err
		}
		ok, err := query.intersects(initial, cmp)
		if err != nil {
			return false, err
		}
		if ok {
			add(initial)
		}
	}

	var exdates []temporal
	for _, p := range e.all("EXDATE") {
		dates, err := dateList(p)
		if err != nil {
			return false, err
		}
		exdates = append(exdates, dates...)
	}
	set = slices.DeleteFunc(set, func(p period) bool { return slices.ContainsFunc(exdates, p.start.equals) })
	for _, p := range e.all("EXRULE") {
		r, err := parseRecur(p.value, opts)
		if err != nil {
			return false, err
		}
		dates, err := r.dates(start, startMinus, q, cmp)
		if err != nil {
			return false, err
		}
		var excluded []period
		for _, d := range dates {
			rp, err := newPeriod(d, duration, wf)
			if err != nil {
				return false, err
			}
			excluded = append(excluded, rp)
		}
		set = slices.DeleteFunc(set, func(p period) bool { return slices.ContainsFunc(excluded, p.equals) })
	}
	return len(set) > 0, nil
}

// duration is RecurrenceSet.Builder's: DURATION, else DTEND minus DTSTART, else zero
// for date-times and one day for dates.
func (e *event) duration(start temporal) (amount, error) {
	if p := e.first("DURATION"); p != nil {
		return parseDuration(p.value)
	}
	endProp := e.first("DTEND")
	if endProp == nil {
		endProp = e.first("DUE")
	}
	if endProp == nil {
		if start.kind == kindDate {
			return amount{period: true, dayN: 1}, nil
		}
		return amount{}, nil
	}
	end, err := dateValue(*endProp)
	if err != nil {
		return amount{}, err
	}
	return between(start, end)
}

// between is TemporalAmountAdapter.between: a Period between two dates, otherwise the
// Duration from start to end (which fails where java.time can't convert end).
func between(start, end temporal) (amount, error) {
	if start.kind == kindDate && end.kind == kindDate {
		return periodBetween(start.local, end.local), nil
	}
	var d time.Duration
	switch start.kind {
	case kindDate:
		return amount{}, errUnsupported
	case kindFloating:
		if end.kind == kindDate {
			return amount{}, errUnsupported
		}
		d = end.local.Sub(start.local)
	default:
		if end.kind != kindOffset && end.kind != kindZoned {
			return amount{}, errUnsupported
		}
		d = end.instant().Sub(start.instant())
	}
	return amount{secs: int64(d / time.Second), nanos: int64(d % time.Second)}, nil
}

// periodBetween is java.time.Period.between for dates.
func periodBetween(a, b time.Time) amount {
	totalMonths := int64(b.Year()-a.Year())*12 + int64(b.Month()-a.Month())
	days := b.Day() - a.Day()
	if totalMonths > 0 && days < 0 {
		totalMonths--
		calc := plusMonths(a, totalMonths)
		days = int(b.Sub(calc).Hours() / 24)
	} else if totalMonths < 0 && days > 0 {
		totalMonths++
		days -= daysIn(b.Year(), b.Month())
	}
	return amount{period: true, years: totalMonths / 12, months: totalMonths % 12, dayN: int64(days)}
}

// dateList is DateList.parse: comma-separated DATE or DATE-TIME values, in the TZID zone
// when there is one.
func dateList(p property) ([]temporal, error) {
	var out []temporal
	for _, v := range strings.Split(p.value, ",") {
		q := p
		q.value = v
		d, err := dateValue(q)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// periodList is RDATE;VALUE=PERIOD: start/end or start/duration pairs.
func periodList(p property) ([]period, error) {
	var out []period
	for _, v := range strings.Split(p.value, ",") {
		startText, endText, ok := strings.Cut(v, "/")
		if !ok {
			return nil, fmt.Errorf("invalid period %q", v)
		}
		q := p
		q.value = startText
		start, err := dateValue(q)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(endText, "P") || strings.HasPrefix(endText, "+P") || strings.HasPrefix(endText, "-P") {
			d, err := parseDuration(endText)
			if err != nil {
				return nil, err
			}
			pp, err := newPeriod(start, d, weekFields{firstDay: 1, minimalDay: 4})
			if err != nil {
				return nil, err
			}
			out = append(out, pp)
			continue
		}
		q.value = endText
		end, err := dateValue(q)
		if err != nil {
			return nil, err
		}
		out = append(out, period{start: start, end: end, noDuration: true})
	}
	return out, nil
}
