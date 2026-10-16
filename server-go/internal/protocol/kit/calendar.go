package kit

import "time"

// gregorianCutover is when java.util.GregorianCalendar switches from the Julian calendar.
var gregorianCutover = time.Date(1582, 10, 15, 0, 0, 0, 0, time.UTC)

// CalendarDate is a lenient GregorianCalendar in UTC with these fields (month from 1;
// overflowing fields carry over). Before 1582-10-15 Java counts in the Julian calendar,
// which garbage input (invalid BCD digits) can reach; Go's time is proleptic Gregorian.
func CalendarDate(year, month, day, hour, minute, second, nanos int) time.Time {
	t := time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC)
	if !t.Before(gregorianCutover) {
		return t
	}
	// Move the month overflow into the year, then count days from the Julian 1st.
	y, m := year+floorDiv(month-1, 12), floorMod(month-1, 12)+1
	first := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	shift := julianDayNumber(y, m, 1) - gregorianDayNumber(y, m, 1)
	return first.AddDate(0, 0, shift).Add(t.Sub(first))
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }

func julianDayNumber(y, m, d int) int {
	a := floorDiv(14-m, 12)
	yy := y + 4800 - a
	mm := m + 12*a - 3
	return d + floorDiv(153*mm+2, 5) + 365*yy + floorDiv(yy, 4) - 32083
}

func gregorianDayNumber(y, m, d int) int {
	a := floorDiv(14-m, 12)
	yy := y + 4800 - a
	mm := m + 12*a - 3
	return d + floorDiv(153*mm+2, 5) + 365*yy + floorDiv(yy, 4) - floorDiv(yy, 100) + floorDiv(yy, 400) - 32045
}
