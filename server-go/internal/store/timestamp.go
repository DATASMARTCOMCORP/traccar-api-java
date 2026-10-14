package store

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// wallClock binds a model.Time to a "timestamp without time zone" column the way the
// Java server does. Java writes java.sql.Timestamp, which stores the wall-clock time of
// the JVM's default zone at millisecond precision (16:40Z is stored as 13:40 in UTC-3),
// and reads it back in that same zone.
//
// Every timestamp column goes through wallClock, as an argument and as a scan target.
// model.Time can't be passed to pgx directly, so a column that skips the conversion
// fails loudly instead of silently storing UTC.
type wallClock struct {
	t   *model.Time
	loc *time.Location
}

func (s *Store) ts(t *model.Time) *wallClock { return &wallClock{t: t, loc: s.loc} }

// TimestampValue implements pgtype.TimestampValuer.
func (w *wallClock) TimestampValue() (pgtype.Timestamp, error) {
	if w.t.IsZero() {
		return pgtype.Timestamp{}, nil
	}
	return pgtype.Timestamp{Time: toWall(w.t.Time, w.loc), Valid: true}, nil
}

// ScanTimestamp implements pgtype.TimestampScanner.
func (w *wallClock) ScanTimestamp(v pgtype.Timestamp) error {
	if !v.Valid {
		*w.t = model.Time{}
		return nil
	}
	*w.t = model.NewTime(fromWall(v.Time, w.loc))
	return nil
}

// toWall returns t's wall-clock time in loc, truncated to milliseconds, as a UTC time
// (pgx writes a timestamp's fields as they are).
func toWall(t time.Time, loc *time.Location) time.Time {
	l := t.Truncate(time.Millisecond).In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}

// fromWall interprets the fields of w (as pgx returns them, in UTC) as a wall-clock
// time in loc, resolving DST edges the way the JVM does:
//   - an ambiguous time (clocks set back) takes the standard offset, the later instant
//   - a time that doesn't exist (clocks set forward) also takes the standard offset
//
// São Paulo had DST until 2019, so stored history contains both cases.
// time.Date makes no promise about which instant it picks, so the choice is explicit.
func fromWall(w time.Time, loc *time.Location) time.Time {
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
