package ical

import "time"

// weekFields is java.time.temporal.WeekFields: the first day of the week (ISO number,
// Monday = 1) and the minimal days in the first week of a year.
type weekFields struct {
	firstDay   int
	minimalDay int
}

// localizedDayOfWeek is the day's number within the week, 1 to 7.
func (w weekFields) localizedDayOfWeek(local time.Time) int {
	return int(floorMod(int64(isoWeekday(local.Weekday())-w.firstDay), 7)) + 1
}

func (w weekFields) startOfWeekOffset(day, dow int) int {
	weekStart := int(floorMod(int64(day-dow), 7))
	offset := -weekStart
	if weekStart+1 > w.minimalDay {
		offset = 7 - weekStart
	}
	return offset
}

func computeWeek(offset, day int) int { return (7 + offset + (day - 1)) / 7 }

// weekOfWeekBasedYear is the localized week-of-week-based-year.
func (w weekFields) weekOfWeekBasedYear(local time.Time) int {
	dow := w.localizedDayOfWeek(local)
	doy := local.YearDay()
	offset := w.startOfWeekOffset(doy, dow)
	week := computeWeek(offset, doy)
	if week == 0 {
		return w.weekOfWeekBasedYear(local.AddDate(0, 0, -doy))
	}
	if week > 50 {
		yearLen := daysInYear(local.Year())
		newYearWeek := computeWeek(offset, yearLen+w.minimalDay)
		if week >= newYearWeek {
			week = week - newYearWeek + 1
		}
	}
	return week
}

// weekBasedYear is the localized week-based-year.
func (w weekFields) weekBasedYear(local time.Time) int {
	dow := w.localizedDayOfWeek(local)
	year := local.Year()
	doy := local.YearDay()
	offset := w.startOfWeekOffset(doy, dow)
	week := computeWeek(offset, doy)
	if week == 0 {
		return year - 1
	}
	yearLen := daysInYear(year)
	if week >= computeWeek(offset, yearLen+w.minimalDay) {
		return year + 1
	}
	return year
}

// weeksInWeekBasedYear is rangeRefinedBy(weekOfWeekBasedYear).getMaximum().
func (w weekFields) weeksInWeekBasedYear(local time.Time) int {
	dow := w.localizedDayOfWeek(local)
	doy := local.YearDay()
	offset := w.startOfWeekOffset(doy, dow)
	week := computeWeek(offset, doy)
	if week == 0 {
		return w.weeksInWeekBasedYear(local.AddDate(0, 0, -(doy + 7)))
	}
	yearLen := daysInYear(local.Year())
	newYearWeek := computeWeek(offset, yearLen+w.minimalDay)
	if week >= newYearWeek {
		return w.weeksInWeekBasedYear(local.AddDate(0, 0, yearLen-doy+1+7))
	}
	return newYearWeek - 1
}

// withDayOfWeek is with(weekFields.dayOfWeek(), v): the day v of the same week.
func (t temporal) withDayOfWeek(v int, w weekFields) temporal {
	current := w.localizedDayOfWeek(t.local)
	if v == current {
		return t
	}
	r, _ := t.plus(int64(v-current), days, w)
	return r
}

// withWeekOfWeekBasedYear is with(weekFields.weekOfWeekBasedYear(), v).
func (t temporal) withWeekOfWeekBasedYear(v int, w weekFields) (temporal, error) {
	if v < 1 || v > 53 {
		return t, errInvalidDate
	}
	current := w.weekOfWeekBasedYear(t.local)
	if v == current {
		return t, nil
	}
	return t.plus(int64(v-current), weeks, w)
}

// plusWeekBasedYears is Recur's weekBasedYears unit: the same week and day of the week
// in another week-based year (the week clamped to that year's last), keeping the time.
func (t temporal) plusWeekBasedYears(n int64, w weekFields) (temporal, error) {
	year := w.weekBasedYear(t.local)
	newYear := int(int64(year) + n)
	if newYear == year {
		return t, nil
	}
	dow := w.localizedDayOfWeek(t.local)
	week := w.weekOfWeekBasedYear(t.local)
	// ofWeekBasedYear
	first := newLocal(newYear, 1, 1, 0, 0, 0, 0)
	offset := w.startOfWeekOffset(1, w.localizedDayOfWeek(first))
	newYearWeek := computeWeek(offset, daysInYear(newYear)+w.minimalDay)
	week = min(week, newYearWeek-1)
	date := first.AddDate(0, 0, -offset+(dow-1)+(week-1)*7)
	return t.withDate(date.Year(), date.Month(), date.Day()), nil
}
