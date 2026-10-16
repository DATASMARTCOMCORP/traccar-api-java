package javatime

import "time"

// FromWall interprets the fields of w (in UTC) as a wall-clock time in loc, as
// java.util.Calendar and java.sql.Timestamp do, resolving DST edges the way the JVM does:
//   - an ambiguous time (clocks set back) takes the standard offset, the later instant
//   - a time that doesn't exist (clocks set forward) also takes the standard offset
//
// São Paulo had DST until 2019, so stored history contains both cases.
// time.Date makes no promise about which instant it picks, so the choice is explicit.
func FromWall(w time.Time, loc *time.Location) time.Time {
	type candidate struct {
		t   time.Time
		dst bool
	}
	var valid []candidate
	standard, haveStandard := 0, false
	seen := map[int]bool{}
	// Collect the offsets in effect around w, each with whether it is a DST offset.
	for _, probe := range []time.Duration{-26 * time.Hour, 0, 26 * time.Hour} {
		p := w.Add(probe).In(loc)
		_, offset := p.Zone()
		if seen[offset] {
			continue
		}
		seen[offset] = true
		if !p.IsDST() && (!haveStandard || offset < standard) {
			standard, haveStandard = offset, true
		}
		// w read with this offset is valid if the zone really has it at that instant.
		t := w.Add(-time.Duration(offset) * time.Second).In(loc)
		if _, actual := t.Zone(); actual == offset {
			valid = append(valid, candidate{t, t.IsDST()})
		}
	}
	switch len(valid) {
	case 1:
		return valid[0].t
	case 0: // gap: clocks jumped over this time
		if !haveStandard {
			_, standard = w.Add(-26 * time.Hour).In(loc).Zone()
		}
		return w.Add(-time.Duration(standard) * time.Second).In(loc)
	}
	// Overlap: prefer standard time; if both or neither are standard, the later instant.
	best := valid[0]
	for _, c := range valid[1:] {
		if best.dst && !c.dst || best.dst == c.dst && c.t.After(best.t) {
			best = c
		}
	}
	return best.t
}
