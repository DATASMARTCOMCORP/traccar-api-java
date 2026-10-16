// Package javatime parses dates the way java.time does for the formats device decoders
// use, including what Java accepts and rejects at the edges (case, signs, offsets, the
// resolver styles) and how it places a local time in a zone at DST changes. A device
// message Java rejects is dropped by Java, so Go must reject exactly the same inputs.
package javatime

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errParse = errors.New("unparseable date")

// parser walks text with java.time's DateTimeFormatter rules.
type parser struct {
	s   string
	pos int
}

func (p *parser) more() bool { return p.pos < len(p.s) }

// literal matches c; case-insensitively when fold is set (parseCaseInsensitive).
func (p *parser) literal(c byte, fold bool) bool {
	if !p.more() {
		return false
	}
	d := p.s[p.pos]
	if d == c || fold && lower(d) == lower(c) {
		p.pos++
		return true
	}
	return false
}

func lower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func digit(c byte) bool { return '0' <= c && c <= '9' }

// fixed parses exactly n digits (appendValue(field, n)).
func (p *parser) fixed(n int) (int, bool) {
	if p.pos+n > len(p.s) {
		return 0, false
	}
	v := 0
	for i := 0; i < n; i++ {
		c := p.s[p.pos+i]
		if !digit(c) {
			return 0, false
		}
		v = 10*v + int(c-'0')
	}
	p.pos += n
	return v, true
}

// year parses appendValue(field, 4, max, SignStyle.EXCEEDS_PAD) in strict mode: 4 digits
// without a sign; more than 4 only with a sign; a minus sign at any width.
func (p *parser) year(max int) (int, bool) {
	start := p.pos
	negative, signed := false, false
	if p.more() && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
		negative, signed = p.s[p.pos] == '-', true
		p.pos++
	}
	digitsStart := p.pos
	v := 0
	for p.more() && digit(p.s[p.pos]) && p.pos-digitsStart < max {
		v = 10*v + int(p.s[p.pos]-'0')
		p.pos++
	}
	n := p.pos - digitsStart
	switch {
	case n < 4:
		p.pos = start
		return 0, false
	case signed && !negative && n <= 4:
		p.pos = start
		return 0, false
	case !signed && n > 4:
		p.pos = start
		return 0, false
	}
	if negative {
		v = -v
	}
	return v, true
}

// fraction parses appendFraction(NANO_OF_SECOND, 0, 9, true): an optional '.' and up to
// nine digits (none is allowed).
func (p *parser) fraction() int {
	if !p.more() || p.s[p.pos] != '.' {
		return 0
	}
	p.pos++
	nanos, n := 0, 0
	for p.more() && digit(p.s[p.pos]) && n < 9 {
		nanos = 10*nanos + int(p.s[p.pos]-'0')
		p.pos++
		n++
	}
	for ; n < 9; n++ {
		nanos *= 10
	}
	return nanos
}

// offset parses appendOffsetId() in lenient mode with noOffsetText "Z": "Z" (any case
// here, as ISO formats parse case-insensitively), "+HH", "+HH:mm" or "+HH:mm:ss".
func (p *parser) offset() (int, bool) {
	if p.literal('Z', true) {
		return 0, true
	}
	if !p.more() || p.s[p.pos] != '+' && p.s[p.pos] != '-' {
		return 0, false
	}
	sign := 1
	if p.s[p.pos] == '-' {
		sign = -1
	}
	start := p.pos
	p.pos++
	hours, ok := p.fixed(2)
	if !ok {
		p.pos = start
		return 0, false
	}
	minutes, seconds := 0, 0
	if m, ok := p.colonPair(); ok {
		minutes = m
		if s, ok := p.colonPair(); ok {
			seconds = s
		}
	}
	total := hours*3600 + minutes*60 + seconds
	// The fields must be in range, and ZoneOffset allows at most ±18:00.
	if hours > 23 || minutes > 59 || seconds > 59 || total > 18*3600 {
		p.pos = start
		return 0, false
	}
	return sign * total, true
}

// colonPair parses ":NN" with NN at most 59, or nothing.
func (p *parser) colonPair() (int, bool) {
	if p.pos+3 > len(p.s) || p.s[p.pos] != ':' || !digit(p.s[p.pos+1]) || !digit(p.s[p.pos+2]) {
		return 0, false
	}
	v := int(p.s[p.pos+1]-'0')*10 + int(p.s[p.pos+2]-'0')
	if v > 59 {
		return 0, false
	}
	p.pos += 3
	return v, true
}

// ParseISOZoned is Instant.from(DateTimeFormatter.ISO_ZONED_DATE_TIME.parse(s))
// (DateUtil.parseDate): "2026-10-01T12:00Z", with optional seconds and fraction, an
// offset, and an optional "[Region/Id]". The instant comes from the offset.
func ParseISOZoned(s string) (time.Time, error) {
	p := &parser{s: s}
	year, ok := p.year(10)
	if !ok || !p.literal('-', false) {
		return time.Time{}, errParse
	}
	month, ok := p.fixed(2)
	if !ok || !p.literal('-', false) {
		return time.Time{}, errParse
	}
	day, ok := p.fixed(2)
	if !ok || !p.literal('T', true) {
		return time.Time{}, errParse
	}
	hour, ok := p.fixed(2)
	if !ok || !p.literal(':', false) {
		return time.Time{}, errParse
	}
	minute, ok := p.fixed(2)
	if !ok {
		return time.Time{}, errParse
	}
	second, nanos := 0, 0
	if p.literal(':', false) {
		if second, ok = p.fixed(2); !ok {
			return time.Time{}, errParse
		}
		nanos = p.fraction()
	}
	offset, ok := p.offset()
	if !ok {
		return time.Time{}, errParse
	}
	if p.more() {
		// optionalStart().appendLiteral('[').parseCaseSensitive().appendZoneRegionId()
		// .appendLiteral(']'): the zone must be one Java knows; it doesn't change the
		// instant, which the offset fixes.
		if !p.literal('[', false) {
			return time.Time{}, errParse
		}
		end := strings.IndexByte(p.s[p.pos:], ']')
		if end < 0 || !validZoneID(p.s[p.pos:p.pos+end]) || p.pos+end+1 != len(p.s) {
			return time.Time{}, errParse
		}
	}
	// ResolverStyle.STRICT: every field in range, the day valid for the month.
	if year < -999999999 || year > 999999999 || month < 1 || month > 12 || day < 1 || day > daysIn(year, month) || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, errParse
	}
	local := time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC)
	return local.Add(-time.Duration(offset) * time.Second), nil
}

// ParseLocal is DateUtil.parse with ofPattern("yyyy-MM-dd HH:mm:ss").withZone(zone):
// case-sensitive, ResolverStyle.SMART (a day past the end of the month is clamped,
// 24:00:00 is the next midnight), and the local time placed in the zone as
// LocalDateTime.atZone does.
func ParseLocal(s string, zone *time.Location) (time.Time, error) {
	p := &parser{s: s}
	year, ok := p.year(19)
	if !ok || !p.literal('-', false) {
		return time.Time{}, errParse
	}
	month, ok := p.fixed(2)
	if !ok || !p.literal('-', false) {
		return time.Time{}, errParse
	}
	day, ok := p.fixed(2)
	if !ok || !p.literal(' ', false) {
		return time.Time{}, errParse
	}
	hour, ok := p.fixed(2)
	if !ok || !p.literal(':', false) {
		return time.Time{}, errParse
	}
	minute, ok := p.fixed(2)
	if !ok || !p.literal(':', false) {
		return time.Time{}, errParse
	}
	second, ok := p.fixed(2)
	if !ok || p.more() {
		return time.Time{}, errParse
	}
	// yyyy is year-of-era: 1 or more (SMART turns it into the year).
	if year < 1 || year > 999999999 || month < 1 || month > 12 || day < 1 || day > 31 || minute > 59 || second > 59 {
		return time.Time{}, errParse
	}
	day = min(day, daysIn(year, month))
	nextDay := false
	switch {
	case hour == 24 && minute == 0 && second == 0:
		hour, nextDay = 0, true
	case hour > 23:
		return time.Time{}, errParse
	}
	t := AtZone(year, time.Month(month), day, hour, minute, second, 0, zone)
	if nextDay {
		t = AtZone(year, time.Month(month), day+1, 0, 0, 0, 0, zone)
	}
	return t, nil
}

func daysIn(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// AtZone is LocalDateTime.atZone (ZonedDateTime.ofLocal without a preferred offset): in
// an overlap the earlier offset wins, and a time in a gap moves forward by the gap.
// time.Date leaves both cases unspecified.
func AtZone(year int, month time.Month, day, hour, minute, second, nanos int, zone *time.Location) time.Time {
	local := time.Date(year, month, day, hour, minute, second, nanos, time.UTC)
	offsetAt := func(t time.Time) time.Duration {
		_, off := t.In(zone).Zone()
		return time.Duration(off) * time.Second
	}
	before := offsetAt(local.Add(-26 * time.Hour))
	after := offsetAt(local.Add(26 * time.Hour))
	var valid []time.Time
	for _, off := range []time.Duration{before, after} {
		t := local.Add(-off)
		if offsetAt(t) == off && (len(valid) == 0 || !valid[0].Equal(t)) {
			valid = append(valid, t)
		}
	}
	switch len(valid) {
	case 0: // gap: shifted by its length, i.e. read with the offset before it
		return local.Add(-before).In(zone)
	case 1:
		return valid[0].In(zone)
	}
	if valid[1].Before(valid[0]) {
		return valid[1].In(zone)
	}
	return valid[0].In(zone)
}

// IsZoneID reports whether id is a region id java.time knows (ZoneId.getAvailableZoneIds).
func IsZoneID(id string) bool {
	_, found := slices.BinarySearch(zoneIDs, id)
	return found
}

// validZoneID is what ZoneIdPrinterParser accepts in brackets: a region id, or an offset
// ("+01:00", "Z") possibly after a UTC, GMT or UT prefix.
func validZoneID(id string) bool {
	if IsZoneID(id) {
		return true
	}
	for _, prefix := range []string{"UTC", "GMT", "UT"} {
		if rest, ok := strings.CutPrefix(id, prefix); ok {
			if rest == "" {
				return true
			}
			id = rest
			break
		}
	}
	p := &parser{s: id}
	if _, ok := p.offset(); ok && !p.more() {
		return true
	}
	return false
}

var customID = regexp.MustCompile(`^GMT([+-])(\d{1,2})(?::?(\d{2}))?$`)

// TimeZone is java.util.TimeZone.getTimeZone(id): a zone id, a custom "GMT+h[h][[:]mm]"
// offset, or GMT for anything else.
func TimeZone(id string) *time.Location {
	if IsZoneID(id) {
		if loc, err := time.LoadLocation(id); err == nil {
			return loc
		}
	}
	if m := customID.FindStringSubmatch(id); m != nil {
		h, _ := strconv.Atoi(m[2])
		mins := 0
		if m[3] != "" {
			mins, _ = strconv.Atoi(m[3])
		}
		if h <= 23 && mins <= 59 {
			off := h*3600 + mins*60
			if m[1] == "-" {
				off = -off
			}
			return time.FixedZone(id, off)
		}
	}
	return time.UTC
}
