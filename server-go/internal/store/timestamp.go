package store

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
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

// fromWall interprets the fields of w as a wall-clock time in loc, as the JVM does (see
// javatime.FromWall).
func fromWall(w time.Time, loc *time.Location) time.Time { return javatime.FromWall(w, loc) }
