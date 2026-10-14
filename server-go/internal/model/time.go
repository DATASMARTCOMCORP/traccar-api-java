package model

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Time is a java.util.Date field: millisecond precision, null when zero.
//
// In JSON it is written as Jackson writes dates, always UTC with milliseconds and a
// "+00:00" offset (2026-10-14T19:11:35.225+00:00), or null. It is read as leniently as
// Jackson's StdDateFormat reads it (see UnmarshalJSON).
//
// The wrapped time is a named field, not embedded, on purpose: an embedded time.Time
// would make Time a fmt.Stringer, and pgx would then silently send its String() form to
// Postgres. Database columns go through internal/store, which applies the zone.
type Time struct {
	Time time.Time
}

// NewTime truncates t to milliseconds, the precision of java.util.Date.
func NewTime(t time.Time) Time {
	if t.IsZero() {
		return Time{}
	}
	return Time{Time: t.Truncate(time.Millisecond)}
}

// IsZero reports whether t is null.
func (t Time) IsZero() bool { return t.Time.IsZero() }

const jsonDateLayout = "2006-01-02T15:04:05.000-07:00"

func (t Time) MarshalJSON() ([]byte, error) {
	return t.appendJSON(nil), nil
}

func (t Time) appendJSON(b []byte) []byte {
	if t.IsZero() {
		return append(b, "null"...)
	}
	b = append(b, '"')
	b = t.Time.UTC().AppendFormat(b, jsonDateLayout)
	return append(b, '"')
}

// UnmarshalJSON accepts what Jackson accepts for a Date (measured against the 6.14.5
// server, see time_test.go):
//   - null or "" → null
//   - a JSON integer, or a string of digits, as epoch milliseconds
//   - ISO-8601: 2026-10-14, 2026-10-14T19:11Z, 2026-10-14T19:11:35, with any number of
//     fraction digits (only milliseconds kept, truncated) and an offset of Z, +03,
//     +0300 or +03:00; no offset means UTC
//   - RFC 1123: Mon, 14 Oct 2030 19:11:35 GMT
//
// Out-of-range fields roll over as in Java's lenient calendar (month 13 is next January).
func (t *Time) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" {
		*t = Time{}
		return nil
	}
	if len(data) > 0 && data[0] != '"' {
		ms, err := strconv.ParseInt(string(data), 10, 64)
		if err != nil {
			return fmt.Errorf("cannot deserialize date from number %s", data)
		}
		*t = Time{Time: time.UnixMilli(ms).UTC()}
		return nil
	}
	s, err := strconv.Unquote(string(data))
	if err != nil {
		return fmt.Errorf("cannot deserialize date from %s", data)
	}
	parsed, err := ParseTime(s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

var (
	isoDate    = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|[+-]\d{2}(?::?\d{2})?)?)?$`)
	epochMilli = regexp.MustCompile(`^-?\d+$`)
)

// ParseTime parses a date string as UnmarshalJSON does.
func ParseTime(s string) (Time, error) {
	if s == "" {
		return Time{}, nil
	}
	if epochMilli.MatchString(s) {
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Time{}, fmt.Errorf("cannot deserialize date from %q", s)
		}
		return Time{Time: time.UnixMilli(ms).UTC()}, nil
	}
	if m := isoDate.FindStringSubmatch(s); m != nil {
		num := func(i int) int {
			n, _ := strconv.Atoi(m[i])
			return n
		}
		ms := 0
		if frac := m[7]; frac != "" {
			frac = (frac + "00")[:3]
			ms, _ = strconv.Atoi(frac)
		}
		loc := time.UTC
		if z := m[8]; z != "" && z != "Z" {
			sign := 1
			if z[0] == '-' {
				sign = -1
			}
			z = z[1:]
			hours, _ := strconv.Atoi(z[:2])
			minutes := 0
			if rest := z[2:]; rest != "" {
				if rest[0] == ':' {
					rest = rest[1:]
				}
				minutes, _ = strconv.Atoi(rest)
			}
			loc = time.FixedZone("", sign*(hours*3600+minutes*60))
		}
		d := time.Date(num(1), time.Month(num(2)), num(3), num(4), num(5), num(6), ms*int(time.Millisecond), loc)
		return Time{Time: d.UTC()}, nil
	}
	if d, err := time.Parse(time.RFC1123, s); err == nil {
		return Time{Time: d.UTC()}, nil
	}
	return Time{}, fmt.Errorf("cannot deserialize date from %q: not a valid representation", s)
}
