package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// The tests in this file need a database written by the Java server, for example side A
// of a kept harness run:
//
//	go run ./compat -pg-bin ... -keep -work /tmp/run      # prints the side_A DSN
//	SIR_TEST_JAVA_DB='postgres://postgres:compat@127.0.0.1:PORT/side_A' \
//	SIR_TEST_TZ=America/Sao_Paulo go test ./internal/store
//
// They never modify that database: they work on a copy (CREATE DATABASE ... TEMPLATE),
// so the user in the DSN must be allowed to create databases. SIR_TEST_TZ is the zone
// the Java server ran in (the harness default is America/Sao_Paulo).

type javaDB struct {
	src  *pgx.Conn // the Java-written database, read only
	copy *pgx.Conn // scratch copy, FK triggers disabled
	st   *Store    // Go store on the copy
}

func openJavaDB(t *testing.T) *javaDB {
	t.Helper()
	dsn := os.Getenv("SIR_TEST_JAVA_DB")
	if dsn == "" {
		t.Skip("SIR_TEST_JAVA_DB not set (see roundtrip_test.go)")
	}
	tz := os.Getenv("SIR_TEST_TZ")
	if tz == "" {
		tz = "America/Sao_Paulo"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	srcName := strings.TrimPrefix(u.Path, "/")
	copyName := fmt.Sprintf("roundtrip_%d", time.Now().UnixNano())

	admin := *u
	admin.Path = "/postgres"
	adminConn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{copyName}.Sanitize()+" TEMPLATE "+pgx.Identifier{srcName}.Sanitize()); err != nil {
		t.Fatalf("copy %s (is something still connected to it?): %v", srcName, err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin.String())
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{copyName}.Sanitize()+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})

	src, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close(context.Background()) })
	copyURL := *u
	copyURL.Path = "/" + copyName
	q := copyURL.Query()
	q.Set("options", "-c session_replication_role=replica")
	copyURL.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	cp, err := pgx.Connect(ctx, copyURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cp.Close(context.Background()) })

	// The Go store connects the way production does: through a JDBC URL.
	password, _ := u.User.Password()
	jdbc := "jdbc:postgresql://" + u.Host + "/" + copyName + "?options=" + url.QueryEscape("-c session_replication_role=replica")
	st, err := Open(ctx, Options{URL: jdbc, User: u.User.Username(), Password: password, MaxPoolSize: 4, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return &javaDB{src: src, copy: cp, st: st}
}

// tableText returns every row of a table as Postgres text, ordered by id.
func tableText(t *testing.T, c *pgx.Conn, table string) []string {
	t.Helper()
	rows, err := c.Query(context.Background(), "SELECT x::text FROM "+table+" x ORDER BY x.id")
	if err != nil {
		t.Fatal(err)
	}
	texts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return texts
}

func (db *javaDB) assertSame(t *testing.T, table, stage string) {
	t.Helper()
	want, got := tableText(t, db.src, table), tableText(t, db.copy, table)
	if slices.Equal(want, got) {
		return
	}
	if len(want) != len(got) {
		t.Errorf("%s after %s: %d rows, Java has %d", table, stage, len(got), len(want))
	}
	for i := range min(len(want), len(got)) {
		if want[i] != got[i] {
			t.Errorf("%s after %s, row %d:\n Java: %s\n   Go: %s", table, stage, i, want[i], got[i])
		}
	}
}

// scramble changes every given column of every row with SQL, so that only a complete
// and exact write by Go can restore the Java text.
func (db *javaDB) scramble(t *testing.T, table string, columns []string) {
	t.Helper()
	ctx := context.Background()
	var sets []string
	for _, c := range columns {
		var typ string
		err := db.copy.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, table, c).Scan(&typ)
		if err != nil {
			t.Fatalf("%s.%s: %v", table, c, err)
		}
		id := pgx.Identifier{c}.Sanitize()
		switch typ {
		case "character varying", "text":
			sets = append(sets, id+" = coalesce("+id+", '') || '~'")
		case "integer", "bigint":
			sets = append(sets, id+" = coalesce("+id+", 0) + 1")
		case "boolean":
			sets = append(sets, id+" = NOT coalesce("+id+", false)")
		case "double precision":
			sets = append(sets, id+" = coalesce("+id+", 0) + 0.5")
		case "timestamp without time zone":
			sets = append(sets, id+" = coalesce("+id+", '2000-01-01') + interval '1 hour 1 millisecond'")
		case "bytea":
			sets = append(sets, id+" = coalesce("+id+", ''::bytea) || '\\x00'::bytea")
		default:
			t.Fatalf("%s.%s: no scramble for %s", table, c, typ)
		}
	}
	if _, err := db.copy.Exec(ctx, "UPDATE "+table+" SET "+strings.Join(sets, ", ")); err != nil {
		t.Fatalf("scramble %s: %v", table, err)
	}
}

// roundTrip checks both write paths for one entity table against the Java rows.
//   - insert: delete every row, then Insert each one again with Go (sequences set so the
//     database hands out the original ids); afterInsert replays targeted writes
//   - update: scramble every ordinary column, then Update each row with Go
func roundTrip[T Entity](t *testing.T, db *javaDB, afterInsert func(t *testing.T, v *T)) int {
	ctx := context.Background()
	tbl := tableOf[T]()
	rows, err := All[T](ctx, db.st)
	if err != nil {
		t.Fatal(err)
	}
	if afterInsert != nil || tbl.name != "tc_servers" {
		if _, err := db.copy.Exec(ctx, "DELETE FROM "+tbl.name); err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			v := &rows[i]
			want := *tbl.id(v)
			if _, err := db.copy.Exec(ctx, "SELECT setval(pg_get_serial_sequence($1, 'id'), $2, false)", tbl.name, want); err != nil {
				t.Fatal(err)
			}
			if err := Insert(ctx, db.st, v); err != nil {
				t.Fatalf("insert %s %d: %v", tbl.name, want, err)
			}
			if got := *tbl.id(v); got != want {
				t.Fatalf("insert %s: id %d, want %d", tbl.name, got, want)
			}
			if afterInsert != nil {
				afterInsert(t, v)
			}
		}
		db.assertSame(t, tbl.name, "Go inserts")
	}

	db.scramble(t, tbl.name, tbl.columns)
	for i := range rows {
		if err := Update(ctx, db.st, &rows[i]); err != nil {
			t.Fatalf("update %s: %v", tbl.name, err)
		}
	}
	db.assertSame(t, tbl.name, "Go updates")
	return len(rows)
}

// differs reports whether the given columns of row id differ between Java's database
// and the copy, which tells which targeted writes Java made to that row.
func (db *javaDB) differs(t *testing.T, table string, id int64, columns ...string) bool {
	t.Helper()
	q := "SELECT ROW(" + strings.Join(columns, ", ") + ")::text FROM " + table + " WHERE id = $1"
	var a, b string
	if err := db.src.QueryRow(context.Background(), q, id).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := db.copy.QueryRow(context.Background(), q, id).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return a != b
}

// TestRoundTripJavaRows is the Step 2 gate: rows written by the Java server, read by Go
// and written back by Go, are stored with identical text.
func TestRoundTripJavaRows(t *testing.T) {
	db := openJavaDB(t)
	ctx := context.Background()
	counts := map[string]int{}

	counts["tc_servers"] = roundTrip[model.Server](t, db, nil)
	counts["tc_users"] = roundTrip(t, db, func(t *testing.T, u *model.User) {
		if u.HashedPassword != nil {
			if err := db.st.UpdateUserPassword(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
	})
	counts["tc_groups"] = roundTrip[model.Group](t, db, func(*testing.T, *model.Group) {})
	counts["tc_devices"] = roundTrip(t, db, func(t *testing.T, d *model.Device) {
		writes := []struct {
			columns []string
			write   func(context.Context, *model.Device) error
		}{
			{[]string{"status", "lastupdate"}, db.st.UpdateDeviceStatus},
			{[]string{"positionid"}, db.st.UpdateDevicePosition},
			{[]string{"motionstreak", "motiontime", "motionlatitude", "motionlongitude", "attributes"},
				func(ctx context.Context, d *model.Device) error { return db.st.UpdateDeviceMotion(ctx, d, true) }},
			{[]string{"motionstate", "motionpositionid", "motiondistance"}, db.st.UpdateDeviceMotionLegacy},
			{[]string{"overspeedstate", "overspeedtime", "overspeedgeofenceid"}, db.st.UpdateDeviceOverspeed},
		}
		for _, w := range writes {
			if db.differs(t, "tc_devices", d.ID, w.columns...) {
				if err := w.write(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	counts["tc_positions"] = roundTrip[model.Position](t, db, func(*testing.T, *model.Position) {})
	counts["tc_events"] = roundTrip[model.Event](t, db, func(*testing.T, *model.Event) {})
	counts["tc_geofences"] = roundTrip[model.Geofence](t, db, func(*testing.T, *model.Geofence) {})
	counts["tc_drivers"] = roundTrip[model.Driver](t, db, func(*testing.T, *model.Driver) {})
	counts["tc_attributes"] = roundTrip[model.ComputedAttribute](t, db, func(*testing.T, *model.ComputedAttribute) {})
	counts["tc_calendars"] = roundTrip[model.Calendar](t, db, func(*testing.T, *model.Calendar) {})
	counts["tc_commands"] = roundTrip[model.Command](t, db, func(*testing.T, *model.Command) {})
	counts["tc_commands_queue"] = roundTrip[model.QueuedCommand](t, db, func(*testing.T, *model.QueuedCommand) {})
	counts["tc_notifications"] = roundTrip[model.Notification](t, db, func(*testing.T, *model.Notification) {})
	counts["tc_maintenances"] = roundTrip[model.Maintenance](t, db, func(*testing.T, *model.Maintenance) {})
	counts["tc_orders"] = roundTrip[model.Order](t, db, func(*testing.T, *model.Order) {})
	counts["tc_reports"] = roundTrip[model.Report](t, db, func(*testing.T, *model.Report) {})
	counts["tc_statistics"] = roundTrip[model.Statistics](t, db, func(*testing.T, *model.Statistics) {})
	counts["tc_actions"] = roundTrip[model.Action](t, db, func(*testing.T, *model.Action) {})
	t.Logf("rows round-tripped per table: %v", counts)

	// The harness scenarios write these; an empty one would make the check vacuous.
	for _, table := range []string{"tc_users", "tc_devices", "tc_positions", "tc_events", "tc_geofences",
		"tc_actions", "tc_commands_queue", "tc_calendars", "tc_notifications"} {
		if counts[table] == 0 {
			t.Errorf("%s has no Java rows; the round trip proves nothing for it", table)
		}
	}
}

// TestReadJavaValues spot-checks decoded values against what the Java API returned for
// the same rows during the harness run.
func TestReadJavaValues(t *testing.T) {
	db := openJavaDB(t)
	ctx := context.Background()
	positions, err := All[model.Position](ctx, db.st)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range positions {
		if p.Protocol != nil && *p.Protocol == "osmand" && p.Latitude == -23.5505 {
			found = true
			// Sent as timestamp=2026-10-01T12:00:00Z; stored as 09:00 wall time (UTC-3).
			if got := p.FixTime.Time.UTC().Format(time.RFC3339); got != "2026-10-01T12:00:00Z" {
				t.Errorf("fixTime = %s", got)
			}
			if v, _ := p.Attributes.Get("batteryLevel"); v != 87.0 {
				t.Errorf("batteryLevel = %#v, want float64 87", v)
			}
		}
	}
	if !found {
		t.Error("first osmand position from the harness not found")
	}
	devices, err := All[model.Device](ctx, db.st)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		if d.Status != nil && strings.TrimSpace(*d.Status) != *d.Status {
			t.Errorf("device %d status %q not trimmed", d.ID, *d.Status)
		}
	}
	if _, err := Get[model.Device](ctx, db.st, 1<<40); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing row: %v", err)
	}
}

// TestModelTimeRejectedByPgx guards the design in wallClock: a model.Time passed to pgx
// without s.ts must fail, not be silently encoded.
func TestModelTimeRejectedByPgx(t *testing.T) {
	db := openJavaDB(t)
	tm := model.NewTime(time.Now())
	var out string
	err := db.copy.QueryRow(context.Background(), "SELECT $1::timestamp::text", tm).Scan(&out)
	if err == nil {
		t.Fatalf("pgx encoded model.Time as %q; it must be passed through Store.ts", out)
	}
}

func TestSchemaCheckOnDatabase(t *testing.T) {
	db := openJavaDB(t)
	ctx := context.Background()
	if err := db.st.CheckSchema(ctx); err != nil {
		t.Fatalf("Java-created schema rejected: %v", err)
	}
	// A changeset from a newer Traccar.
	if _, err := db.copy.Exec(ctx, `INSERT INTO databasechangelog (id, author, filename, dateexecuted, orderexecuted, exectype)
		VALUES ('changelog-9.9.9', 'author', 'changelog-9.9.9', now(), 999, 'EXECUTED')`); err != nil {
		t.Fatal(err)
	}
	if err := db.st.CheckSchema(ctx); !errors.Is(err, ErrSchema) {
		t.Errorf("newer schema accepted: %v", err)
	}
	if _, err := db.copy.Exec(ctx, "DELETE FROM databasechangelog WHERE id IN ('changelog-9.9.9', 'changelog-6.13.0')"); err != nil {
		t.Fatal(err)
	}
	if err := db.st.CheckSchema(ctx); !errors.Is(err, ErrSchema) {
		t.Errorf("older schema accepted: %v", err)
	}
	if _, err := db.copy.Exec(ctx, `INSERT INTO databasechangelog (id, author, filename, dateexecuted, orderexecuted, exectype)
		VALUES ('changelog-6.13.0', 'author', 'changelog-6.13.0', now(), 32, 'EXECUTED')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.copy.Exec(ctx, "UPDATE databasechangeloglock SET locked = true"); err != nil {
		t.Fatal(err)
	}
	if err := db.st.CheckSchema(ctx); !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), "lock") {
		t.Errorf("Liquibase lock ignored: %v", err)
	}
}
