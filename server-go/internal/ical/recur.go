package ical

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// frequency is Recur.Frequency.
type frequency uint8

const (
	secondly frequency = iota
	minutely
	hourly
	daily
	weekly
	monthly
	yearly
)

var frequencies = map[string]frequency{"SECONDLY": secondly, "MINUTELY": minutely, "HOURLY": hourly,
	"DAILY": daily, "WEEKLY": weekly, "MONTHLY": monthly, "YEARLY": yearly}

// weekDay is a BYDAY entry: an ISO day of the week with an optional ordinal ("-1FR").
type weekDay struct {
	day    int // ISO, Monday = 1
	offset int
}

var dayNames = map[string]int{"MO": 1, "TU": 2, "WE": 3, "TH": 4, "FR": 5, "SA": 6, "SU": 7}

// recur is an RRULE or EXRULE (ical4j's Recur) and the date generation of its
// getDates.
type recur struct {
	freq       frequency
	until      *temporal
	count      int // -1: unset
	interval   int // -1: unset
	seconds    []int
	minutes    []int
	hours      []int
	days       []weekDay
	monthDays  []int
	yearDays   []int
	weekNos    []int
	monthList  []int
	setPos     []int
	weekStart  int // ISO day; 0 when unset
	incUnit    unit
	localeWeek weekFields // WeekFields.of(Locale.getDefault())
	defaultTZ  *zone
}

// parseRecur is new Recur(String) without experimental tokens: names are
// case-insensitive, and anything unknown or out of range fails, as in ical4j.
func parseRecur(value string, opts Options) (*recur, error) {
	r := &recur{count: -1, interval: -1, localeWeek: opts.localeWeek(), defaultTZ: opts.defaultZone()}
	tokens := javaSplitRecur(value)
	hasFreq := false
	for i := 0; i < len(tokens); i++ {
		name := strings.ToUpper(tokens[i])
		if i+1 >= len(tokens) {
			return nil, fmt.Errorf("missing expected token, last token: %s", tokens[i])
		}
		i++
		v := tokens[i]
		var err error
		switch name {
		case "FREQ":
			f, ok := frequencies[strings.ToUpper(v)]
			if !ok {
				return nil, fmt.Errorf("invalid FREQ %q", v)
			}
			r.freq, hasFreq = f, true
		case "UNTIL":
			u, perr := parseValue(v, nil)
			if perr != nil {
				return nil, perr
			}
			r.until = &u
		case "COUNT":
			n, perr := strconv.ParseInt(v, 10, 32)
			if perr != nil {
				return nil, perr
			}
			r.count = int(n)
		case "INTERVAL":
			n, perr := strconv.ParseInt(v, 10, 32)
			if perr != nil {
				return nil, perr
			}
			r.interval = int(n)
		case "BYSECOND":
			r.seconds, err = numberList(v, 0, 59, false)
		case "BYMINUTE":
			r.minutes, err = numberList(v, 0, 59, false)
		case "BYHOUR":
			r.hours, err = numberList(v, 0, 23, false)
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				if d == "" {
					continue
				}
				wd, perr := parseWeekDay(d)
				if perr != nil {
					return nil, perr
				}
				r.days = append(r.days, wd)
			}
		case "BYMONTHDAY":
			r.monthDays, err = numberList(v, 1, 31, true)
		case "BYYEARDAY":
			r.yearDays, err = numberList(v, 1, 366, true)
		case "BYWEEKNO":
			r.weekNos, err = numberList(v, 1, 53, true)
		case "BYMONTH":
			for _, m := range strings.Split(v, ",") {
				if strings.HasSuffix(m, "L") {
					return nil, errors.New("leap months are not supported")
				}
				n, perr := strconv.ParseInt(m, 10, 32)
				if perr != nil {
					return nil, perr
				}
				if n < 1 || n > 12 {
					return nil, fmt.Errorf("value not in range: %d", n)
				}
				r.monthList = append(r.monthList, int(n))
			}
		case "BYSETPOS":
			r.setPos, err = numberList(v, 1, 366, true)
		case "WKST":
			d, ok := dayNames[strings.ToUpper(v)]
			if !ok {
				return nil, fmt.Errorf("invalid WKST %q", v)
			}
			r.weekStart = d
		default:
			return nil, fmt.Errorf("invalid recurrence rule part: %s=%s", tokens[i-1], v)
		}
		if err != nil {
			return nil, err
		}
	}
	if !hasFreq {
		return nil, errors.New("a recurrence rule MUST contain a FREQ rule part")
	}
	switch r.freq {
	case secondly:
		r.incUnit = seconds
	case minutely:
		r.incUnit = minutes
	case hourly:
		r.incUnit = hours
	case daily:
		r.incUnit = days
	case weekly:
		r.incUnit = weeks
	case monthly:
		r.incUnit = months
	case yearly:
		r.incUnit = years
		if len(r.weekNos) > 0 {
			r.incUnit = weekBasedYears
		}
	}
	return r, nil
}

// javaSplitRecur is value.split("[;=]"): empty strings between separators are kept, and
// trailing ones dropped.
func javaSplitRecur(value string) []string {
	var out []string
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] == ';' || value[i] == '=' {
			out = append(out, value[start:i])
			start = i + 1
		}
	}
	out = append(out, value[start:])
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// numberList is ical4j's NumberList: comma-separated integers with an optional sign;
// values outside [min, max] (by absolute value) fail, as do negatives where not allowed.
func numberList(v string, lo, hi int, negatives bool) ([]int, error) {
	var out []int
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimPrefix(s, "+")
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return nil, err
		}
		if n < 0 && !negatives {
			return nil, fmt.Errorf("negative value not allowed: %d", n)
		}
		abs := n
		if abs < 0 {
			abs = -abs
		}
		if int(abs) < lo || int(abs) > hi {
			return nil, fmt.Errorf("value not in range: %d", n)
		}
		out = append(out, int(n))
	}
	return out, nil
}

// parseWeekDay is new WeekDay("-1FR").
func parseWeekDay(s string) (weekDay, error) {
	if len(s) < 2 {
		return weekDay{}, fmt.Errorf("invalid BYDAY %q", s)
	}
	d, ok := dayNames[strings.ToUpper(s[len(s)-2:])]
	if !ok {
		return weekDay{}, fmt.Errorf("invalid BYDAY %q", s)
	}
	wd := weekDay{day: d}
	if len(s) > 2 {
		n, err := strconv.ParseInt(strings.TrimPrefix(s[:len(s)-2], "+"), 10, 32)
		if err != nil {
			return weekDay{}, err
		}
		wd.offset = int(n)
	}
	return wd, nil
}

func (r *recur) intervalOrOne() int { return max(r.interval, 1) }

// weekStartFields are the WeekFields of BYWEEKNO and of yearly week increments: WKST (or
// Monday) with 4 minimal days.
func (r *recur) weekNoFields() weekFields {
	first := 1
	if r.weekStart != 0 {
		first = r.weekStart
	}
	return weekFields{firstDay: first, minimalDay: 4}
}

// byDayFields are the WeekFields of BYDAY: WKST with 1 minimal day, or the locale's.
func (r *recur) byDayFields() weekFields {
	if r.weekStart != 0 {
		return weekFields{firstDay: r.weekStart, minimalDay: 1}
	}
	return r.localeWeek
}

func (r *recur) increment(seed temporal, multiplier int) (temporal, error) {
	return seed.plus(int64(r.intervalOrOne()*multiplier), r.incUnit, r.weekNoFields())
}

// filterType is deriveFilterType: the frequency the BYDAY rule treats as its scope.
func (r *recur) filterType() frequency {
	switch {
	case r.freq == daily || len(r.yearDays) > 0 || len(r.monthDays) > 0:
		return daily
	case r.freq == weekly || len(r.weekNos) > 0:
		return weekly
	case r.freq == monthly || len(r.monthList) > 0:
		return monthly
	}
	return r.freq
}

// candidates is Recur.getCandidates: the dates the BY rules make from one increment of
// the seed, sorted.
func (r *recur) candidates(rootSeed, date temporal, cmp comparator) ([]temporal, error) {
	dates := []temporal{date}
	var err error
	if len(r.monthList) > 0 {
		if dates, err = r.byMonth(dates); err != nil {
			return nil, err
		}
	}
	if len(r.weekNos) > 0 {
		if dates, err = r.byWeekNo(dates); err != nil {
			return nil, err
		}
	}
	if len(r.yearDays) > 0 {
		if dates, err = r.byYearDay(dates); err != nil {
			return nil, err
		}
	}
	if len(r.monthDays) > 0 {
		if dates, err = byMonthDay(dates, r.monthDays, r.freq); err != nil {
			return nil, err
		}
	} else if r.freq == monthly && len(r.days) == 0 ||
		r.freq == yearly && len(r.yearDays) == 0 && len(r.weekNos) == 0 && len(r.days) == 0 {
		// The seed's day of the month, as TemporalAdapter.toLocalTime sees it.
		day := cmp.toLocalTime(rootSeed).local.Day()
		if dates, err = byMonthDay(dates, []int{day}, r.freq); err != nil {
			return nil, err
		}
	}
	if len(r.days) > 0 {
		if dates, err = r.byDay(dates, r.days, r.filterType()); err != nil {
			return nil, err
		}
	} else if r.freq == weekly || r.freq == yearly && len(r.yearDays) == 0 && len(r.weekNos) > 0 && len(r.monthDays) == 0 {
		dow, err := rootSeed.get(dayOfWeek)
		if err != nil {
			return nil, err
		}
		if dates, err = r.byDay(dates, []weekDay{{day: dow}}, r.filterType()); err != nil {
			return nil, err
		}
	}
	if len(r.hours) > 0 {
		if dates, err = byTime(dates, r.hours, hourOfDay, r.freq >= daily); err != nil {
			return nil, err
		}
	}
	if len(r.minutes) > 0 {
		if dates, err = byTime(dates, r.minutes, minuteOfHour, r.freq >= hourly); err != nil {
			return nil, err
		}
	}
	if len(r.seconds) > 0 {
		if dates, err = byTime(dates, r.seconds, secondOfMinute, r.freq != secondly); err != nil {
			return nil, err
		}
	}
	if len(r.setPos) > 0 && len(dates) > 0 {
		if err := sortTemporals(dates, cmp); err != nil {
			return nil, err
		}
		var picked []temporal
		for _, p := range r.setPos {
			switch {
			case p > 0 && p <= len(dates):
				picked = append(picked, dates[p-1])
			case p < 0 && p >= -len(dates):
				picked = append(picked, dates[len(dates)+p])
			}
		}
		dates = picked
	}
	if err := sortTemporals(dates, cmp); err != nil {
		return nil, err
	}
	return dates, nil
}

// sortTemporals is List.sort with a TemporalComparator (stable); a comparison Java can't
// make fails the sort.
func sortTemporals(dates []temporal, cmp comparator) error {
	var err error
	slices.SortStableFunc(dates, func(a, b temporal) int {
		c, e := cmp.compare(a, b)
		if e != nil && err == nil {
			err = e
		}
		return c
	})
	return err
}

func (r *recur) byMonth(dates []temporal) ([]temporal, error) {
	var out []temporal
	for _, d := range dates {
		if r.freq == yearly {
			for _, m := range r.monthList {
				c, err := d.with(monthOfYear, m)
				if err != nil {
					return nil, err
				}
				out = append(out, c)
			}
			continue
		}
		m, _ := d.get(monthOfYear)
		if slices.Contains(r.monthList, m) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *recur) byWeekNo(dates []temporal) ([]temporal, error) {
	wf := r.weekNoFields()
	var out []temporal
	for _, d := range dates {
		weeksInYear := wf.weeksInWeekBasedYear(d.local)
		for _, n := range r.weekNos {
			if n == 0 || n < -weeksInYear || n > weeksInYear {
				continue
			}
			target := n
			if n < 0 {
				target = weeksInYear + 1 + n
			}
			c, err := d.withWeekOfWeekBasedYear(target, wf)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *recur) byYearDay(dates []temporal) ([]temporal, error) {
	var out []temporal
	for _, d := range dates {
		if r.freq == yearly {
			length := daysInYear(d.local.Year())
			for _, n := range r.yearDays {
				if n == 0 || n < -length || n > length {
					continue
				}
				target := n
				if n < 0 {
					target = length + 1 + n
				}
				c, err := d.with(dayOfYear, target)
				if err != nil {
					return nil, err
				}
				out = append(out, c)
			}
			continue
		}
		if slices.Contains(r.yearDays, d.local.YearDay()) {
			out = append(out, d)
		}
	}
	return out, nil
}

// byMonthDay is ByMonthDayRule (SKIP=OMIT): expansion for monthly and yearly rules,
// a filter otherwise.
func byMonthDay(dates []temporal, list []int, freq frequency) ([]temporal, error) {
	var out []temporal
	for _, d := range dates {
		year, month := d.local.Year(), d.local.Month()
		length := daysIn(year, month)
		if freq == monthly || freq == yearly {
			maxLength := daysIn(2000, month) // Month.maxLength: February counts 29
			for _, n := range list {
				abs := n
				if abs < 0 {
					abs = -abs
				}
				if maxLength < abs {
					continue
				}
				var target int
				switch {
				case n > 0 && length < n, n < 0 && -length > n:
					continue
				case n > 0:
					target = n
				default:
					target = length + 1 + n
				}
				c, err := d.with(dayOfMonth, target)
				if err != nil {
					return nil, err
				}
				out = append(out, c)
			}
			continue
		}
		day := d.local.Day()
		if slices.Contains(list, day) || slices.Contains(list, -(length-day+1)) {
			out = append(out, d)
		}
	}
	return out, nil
}

// byDay is ByDayRule: expansion within the week, month or year for those frequencies (a
// filter otherwise), then the ordinal of each listed day.
func (r *recur) byDay(dates []temporal, list []weekDay, scope frequency) ([]temporal, error) {
	wf := r.byDayFields()
	listed := func(d temporal) bool {
		dow := isoWeekday(d.local.Weekday())
		return slices.ContainsFunc(list, func(w weekDay) bool { return w.day == dow })
	}
	var out []temporal
	for _, d := range dates {
		var expanded []temporal
		switch scope {
		case weekly:
			for i := 1; i <= 7; i++ {
				if c := d.withDayOfWeek(i, wf); listed(c) {
					expanded = append(expanded, c)
				}
			}
		case monthly:
			for i := 1; i <= daysIn(d.local.Year(), d.local.Month()); i++ {
				c, err := d.with(dayOfMonth, i)
				if err != nil {
					return nil, err
				}
				if listed(c) {
					expanded = append(expanded, c)
				}
			}
		case yearly:
			for i := 1; i <= daysInYear(d.local.Year()); i++ {
				c, err := d.with(dayOfYear, i)
				if err != nil {
					return nil, err
				}
				if listed(c) {
					expanded = append(expanded, c)
				}
			}
		default:
			// LimitFilter: dayList.contains(WeekDay(day)) also compares the ordinal, so
			// only entries without one match.
			dow := isoWeekday(d.local.Weekday())
			if slices.Contains(list, weekDay{day: dow}) {
				expanded = []temporal{d}
			}
		}
		for _, w := range list {
			var same []temporal
			for _, c := range expanded {
				if isoWeekday(c.local.Weekday()) == w.day {
					same = append(same, c)
				}
			}
			switch {
			case w.offset == 0:
				out = append(out, same...)
			case w.offset < 0 && w.offset >= -len(same):
				out = append(out, same[len(same)+w.offset])
			case w.offset > 0 && w.offset <= len(same):
				out = append(out, same[w.offset-1])
			}
		}
	}
	return out, nil
}

// byTime is ByHourRule, ByMinuteRule and BySecondRule: expansion when expand is set,
// a filter otherwise.
func byTime(dates []temporal, list []int, f field, expand bool) ([]temporal, error) {
	var out []temporal
	for _, d := range dates {
		if expand {
			for _, v := range list {
				c, err := d.with(f, v)
				if err != nil {
					return nil, err
				}
				out = append(out, c)
			}
			continue
		}
		v, err := d.get(f)
		if err != nil {
			return nil, err
		}
		if slices.Contains(list, v) {
			out = append(out, d)
		}
	}
	return out, nil
}

// maxIncrementCount is Recur's default net.fortuna.ical4j.recur.maxincrementcount.
const maxIncrementCount = 1000

// dates is Recur.getDates(seed, periodStart, periodEnd): the occurrences from seed that
// fall within [periodStart, periodEnd] (and UNTIL), honoring COUNT.
func (r *recur) dates(seed, periodStart, periodEnd temporal, cmp comparator) ([]temporal, error) {
	isBefore := func(a, b temporal) (bool, error) {
		c, err := cmp.compare(a, b)
		return c < 0, err
	}
	isAfter := func(a, b temporal) (bool, error) {
		c, err := cmp.compare(a, b)
		return c > 0, err
	}

	candidateSeed := seed
	multiplier := 1
	if r.count < 0 {
		// Skip ahead to just before the period (doubling, then a binary search).
		limit, err := periodStart.plus(-int64(r.intervalOrOne()), r.incUnit, r.weekNoFields())
		if err != nil {
			return nil, err
		}
		incremented, err := r.increment(seed, multiplier)
		if err != nil {
			return nil, err
		}
		for {
			before, err := isBefore(incremented, limit)
			if err != nil {
				return nil, err
			}
			if !before {
				break
			}
			candidateSeed = incremented
			multiplier *= 2
			if incremented, err = r.increment(seed, multiplier); err != nil {
				return nil, err
			}
		}
		low, high := max(1, multiplier/2), multiplier
		for low < high {
			mid := (low + high) / 2
			incremented, err := r.increment(seed, mid)
			if err != nil {
				return nil, err
			}
			before, err := isBefore(incremented, limit)
			if err != nil {
				return nil, err
			}
			if before {
				candidateSeed = incremented
				low = mid + 1
			} else {
				high = mid
			}
		}
		multiplier = low
	}

	var (
		result     []temporal
		generated  int
		invalid    = map[temporalKey]bool{} // HashSet<T>: equal values count once
		noCands    int
		last       *temporal
		candidates []temporal
	)
	withinEnd := func(c *temporal) (bool, error) {
		if c == nil {
			return generated+len(invalid) < r.count || r.count < 1, nil
		}
		if r.until != nil {
			after, err := isAfter(*c, *r.until)
			if err != nil {
				return false, err
			}
			if after {
				return false, nil
			}
		}
		after, err := isAfter(*c, periodEnd)
		if err != nil {
			return false, err
		}
		if after {
			return false, nil
		}
		if r.count >= 1 && generated+len(invalid) >= r.count {
			return false, nil
		}
		return true, nil
	}
	for {
		advance, err := withinEnd(last)
		if err != nil {
			return nil, err
		}
		if advance {
			for len(candidates) == 0 {
				if candidates, err = r.candidates(seed, candidateSeed, cmp); err != nil {
					return nil, err
				}
				if len(candidates) == 0 {
					noCands++
					within, err := withinEnd(&candidateSeed)
					if err != nil {
						return nil, err
					}
					if noCands > maxIncrementCount || !within {
						advance = false
						break
					}
				} else {
					noCands = 0
				}
				if candidateSeed, err = r.increment(seed, multiplier); err != nil {
					return nil, err
				}
				multiplier++
			}
		}
		if !advance {
			break
		}
		c := candidates[0]
		candidates = candidates[1:]
		last = &c
		beforeSeed, err := isBefore(c, seed)
		if err != nil {
			return nil, err
		}
		if beforeSeed {
			continue
		}
		beforeStart, err := isBefore(c, periodStart)
		if err != nil {
			return nil, err
		}
		afterEnd, err := isAfter(c, periodEnd)
		if err != nil {
			return nil, err
		}
		if beforeStart || afterEnd {
			invalid[c.key()] = true
			continue
		}
		if r.until != nil {
			afterUntil, err := isAfter(c, *r.until)
			if err != nil {
				return nil, err
			}
			if afterUntil {
				continue
			}
		}
		generated++
		result = append(result, c)
	}
	if err := sortTemporals(result, cmp); err != nil {
		return nil, err
	}
	return result, nil
}
