package ical

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// TemporalAmountAdapter.parse: values ending in W or D are java.time.Periods.
	periodLike = regexp.MustCompile(`^[+-]?P.*[WD]$`)
	periodRE   = regexp.MustCompile(`(?i)^([-+]?)P(?:([-+]?[0-9]+)Y)?(?:([-+]?[0-9]+)M)?(?:([-+]?[0-9]+)W)?(?:([-+]?[0-9]+)D)?$`)
	durationRE = regexp.MustCompile(`(?i)^([-+]?)P(?:([-+]?[0-9]+)D)?(T(?:([-+]?[0-9]+)H)?(?:([-+]?[0-9]+)M)?(?:([-+]?[0-9]+)(?:[.,]([0-9]{0,9}))?S)?)?$`)
)

// parseDuration is TemporalAmountAdapter.parse (strict): java.time.Period.parse for
// values ending in W or D, java.time.Duration.parse otherwise.
func parseDuration(value string) (amount, error) {
	num := func(s string) (int64, error) {
		if s == "" {
			return 0, nil
		}
		return strconv.ParseInt(s, 10, 64)
	}
	if periodLike.MatchString(value) {
		m := periodRE.FindStringSubmatch(value)
		if m == nil || m[2] == "" && m[3] == "" && m[4] == "" && m[5] == "" {
			return amount{}, fmt.Errorf("invalid period %q", value)
		}
		var v [4]int64
		for i := range v {
			n, err := num(m[i+2])
			if err != nil {
				return amount{}, err
			}
			v[i] = n
		}
		a := amount{period: true, years: v[0], months: v[1], dayN: v[2]*7 + v[3]}
		if m[1] == "-" {
			a = a.negated()
		}
		return a, nil
	}
	m := durationRE.FindStringSubmatch(value)
	if m == nil || m[3] == "T" || m[2] == "" && m[3] == "" {
		return amount{}, fmt.Errorf("invalid duration %q", value)
	}
	d, err := num(m[2])
	if err != nil {
		return amount{}, err
	}
	h, err := num(m[4])
	if err != nil {
		return amount{}, err
	}
	mi, err := num(m[5])
	if err != nil {
		return amount{}, err
	}
	s, err := num(m[6])
	if err != nil {
		return amount{}, err
	}
	var nanos int64
	if m[7] != "" {
		frac := m[7] + strings.Repeat("0", 9-len(m[7]))
		if nanos, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return amount{}, err
		}
		if strings.HasPrefix(m[6], "-") {
			nanos = -nanos
		}
	}
	total := d*86400 + h*3600 + mi*60 + s
	if m[1] == "-" {
		total, nanos = -total, -nanos
	}
	// Normalize like Duration.ofSeconds(seconds, nanoAdjustment).
	total += nanos / 1e9
	nanos %= 1e9
	if nanos < 0 {
		nanos += 1e9
		total--
	}
	return amount{secs: total, nanos: nanos}, nil
}
