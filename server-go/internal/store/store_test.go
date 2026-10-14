package store

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

func TestDSNFromJDBC(t *testing.T) {
	tests := []struct {
		jdbc, dsn string
		ignored   []string
	}{
		{"jdbc:postgresql://db.example:5432/traccar", "postgres://db.example:5432/traccar?sslmode=prefer", nil},
		{"jdbc:postgresql://db.example/traccar?ssl=true", "postgres://db.example/traccar?sslmode=verify-full", nil},
		{"jdbc:postgresql://db/traccar?ssl=true&sslmode=require", "postgres://db/traccar?sslmode=require", nil},
		{"jdbc:postgresql:traccar", "postgres://localhost/traccar?sslmode=prefer", nil},
		{"jdbc:postgresql://db/traccar?ApplicationName=sir&connectTimeout=5&prepareThreshold=0",
			"postgres://db/traccar?application_name=sir&connect_timeout=5&sslmode=prefer", []string{"prepareThreshold"}},
		{"jdbc:postgresql://db/traccar?currentSchema=sir", "postgres://db/traccar?search_path=sir&sslmode=prefer", nil},
		{"jdbc:postgresql://db/traccar?options=-c%20statement_timeout%3D5000",
			"postgres://db/traccar?options=-c%20statement_timeout%3D5000&sslmode=prefer", nil},
	}
	for _, tt := range tests {
		dsn, ignored, err := DSNFromJDBC(tt.jdbc)
		if err != nil || dsn != tt.dsn || !slices.Equal(ignored, tt.ignored) {
			t.Errorf("DSNFromJDBC(%s) = %s, %v, %v; want %s, %v", tt.jdbc, dsn, ignored, err, tt.dsn, tt.ignored)
		}
	}
	for _, bad := range []string{"jdbc:h2:./data/database", "postgres://db/x", "jdbc:mysql://db/x"} {
		if _, _, err := DSNFromJDBC(bad); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
}

func TestWallClockJavaVectors(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	// Stored wall time → instant the Java 6.14.5 server returned in the API (JVM in
	// America/Sao_Paulo). 2018-11-04 00:30 doesn't exist (DST start); 2019-02-16 23:30
	// happened twice (DST end).
	tests := []struct{ wall, utc string }{
		{"2019-02-16 23:30:00.123", "2019-02-17T02:30:00.123Z"}, // ambiguous → standard (-03)
		{"2019-02-16 22:30:00", "2019-02-17T00:30:00Z"},         // still DST (-02)
		{"2018-11-04 00:30:00", "2018-11-04T03:30:00Z"},         // gap → standard offset
		{"2018-11-03 23:59:59", "2018-11-04T02:59:59Z"},
		{"2019-02-17 00:30:00", "2019-02-17T03:30:00Z"},
		{"2019-02-16 23:59:59.999", "2019-02-17T02:59:59.999Z"},
		{"2026-10-14 13:40:00", "2026-10-14T16:40:00Z"}, // today: no DST
	}
	for _, tt := range tests {
		w, _ := time.Parse("2006-01-02 15:04:05.999", tt.wall)
		want, _ := time.Parse(time.RFC3339Nano, tt.utc)
		got := fromWall(w, sp)
		if !got.Equal(want) {
			t.Errorf("fromWall(%s) = %s, want %s", tt.wall, got.UTC().Format(time.RFC3339Nano), tt.utc)
		}
	}
}

func TestWallClockRoundTrip(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	// Writing is the inverse of reading for every instant that isn't in a DST overlap's
	// first pass; Java loses that hour the same way.
	start := time.Date(2018, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 24 * 200 {
		instant := start.Add(time.Duration(i)*time.Hour + 17*time.Minute + 123*time.Millisecond + 456*time.Microsecond)
		w := toWall(instant, sp)
		back := fromWall(w, sp)
		want := instant.Truncate(time.Millisecond)
		overlapFirstPass := back.Sub(want) == time.Hour && want.In(sp).IsDST()
		if !back.Equal(want) && !overlapFirstPass {
			t.Fatalf("%s → %s → %s", instant, w, back)
		}
	}
	if got := toWall(time.Date(2026, 10, 14, 16, 40, 0, 999_999_999, time.UTC), sp); got.Format("15:04:05.000000") != "13:40:00.999000" {
		t.Errorf("toWall must convert to the zone and truncate to ms: %s", got)
	}
	if got := fromWall(time.Date(2026, 10, 14, 16, 40, 0, 0, time.UTC), time.UTC); !got.Equal(time.Date(2026, 10, 14, 16, 40, 0, 0, time.UTC)) {
		t.Errorf("UTC: %s", got)
	}
}

func TestWallClockAdapter(t *testing.T) {
	s := &Store{loc: time.FixedZone("UTC-3", -3*3600)}
	var tm model.Time
	w := s.ts(&tm)
	if v, _ := w.TimestampValue(); v.Valid {
		t.Error("zero model.Time must be NULL")
	}
	tm = model.NewTime(time.Date(2026, 10, 14, 16, 40, 0, 0, time.UTC))
	if v, _ := w.TimestampValue(); !v.Valid || v.Time.Format("2006-01-02 15:04") != "2026-10-14 13:40" {
		t.Errorf("value = %+v", v)
	}
}

func TestCheckChangesets(t *testing.T) {
	var postgres []changeset
	for _, k := range knownChangesets {
		if k.onPostgres && !k.optional {
			postgres = append(postgres, k.changeset)
		}
	}
	if len(postgres) != 34 {
		t.Fatalf("a Postgres database from the current changelog has 34 rows; list has %d", len(postgres))
	}
	if err := checkChangesets(postgres); err != nil {
		t.Errorf("current schema: %v", err)
	}
	withDSC := append(slices.Clone(postgres), changeset{"changelog-6.13-dsc", "dsc-trackhub", "changelog-6.13-dsc"})
	if err := checkChangesets(withDSC); err != nil {
		t.Errorf("with the SIR changeset: %v", err)
	}
	newer := append(slices.Clone(postgres), changeset{"changelog-6.16.0", "author", "changelog-6.16.0"})
	if err := checkChangesets(newer); !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer schema: %v", err)
	}
	older := postgres[:len(postgres)-1] // without 6.15.0
	if err := checkChangesets(older); !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), "changelog-6.15.0") {
		t.Errorf("older schema: %v", err)
	}
	renamed := slices.Clone(postgres)
	renamed[0].author = "someone"
	if err := checkChangesets(renamed); !errors.Is(err, ErrSchema) {
		t.Error("author is part of a changeset's identity")
	}
}

// TestKnownChangesetsMatchChangelogs checks knownChangesets against the Liquibase files
// in schema/, so the list can't drift from the changelog Java runs.
func TestKnownChangesetsMatchChangelogs(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "schema")
	master, err := os.ReadFile(filepath.Join(dir, "changelog-master.xml"))
	if err != nil {
		t.Skip("schema directory not found:", err)
	}
	type changeLog struct {
		LogicalFilePath string `xml:"logicalFilePath,attr"`
		Includes        []struct {
			File string `xml:"file,attr"`
		} `xml:"include"`
		ChangeSets []struct {
			ID     string `xml:"id,attr"`
			Author string `xml:"author,attr"`
			DBMS   string `xml:"dbms,attr"`
		} `xml:"changeSet"`
	}
	var m changeLog
	if err := xml.Unmarshal(master, &m); err != nil {
		t.Fatal(err)
	}
	files := []string{}
	for _, inc := range m.Includes {
		files = append(files, inc.File)
	}
	files = append(files, "changelog-6.13-dsc.xml") // SIR changeset, see knownChangesets
	var fromXML []knownChangeset
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		// encoding/xml only reads XML 1.0; the 1.1 header in some files changes nothing here.
		data = []byte(strings.Replace(string(data), `version="1.1"`, `version="1.0"`, 1))
		var cl changeLog
		if err := xml.Unmarshal(data, &cl); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, cs := range cl.ChangeSets {
			fromXML = append(fromXML, knownChangeset{
				changeset{cs.ID, cs.Author, cl.LogicalFilePath}, runsOnPostgres(cs.DBMS), f == "changelog-6.13-dsc.xml"})
		}
	}
	if !slices.Equal(fromXML, knownChangesets) {
		t.Errorf("knownChangesets differs from schema/*.xml:\n xml: %v\n  go: %v", fromXML, knownChangesets)
	}
}

// runsOnPostgres evaluates a Liquibase dbms attribute: a comma-separated list where
// "!name" excludes a database and an empty list means all.
func runsOnPostgres(dbms string) bool {
	if dbms == "" {
		return true
	}
	included, onlyExclusions := false, true
	for _, d := range strings.Split(dbms, ",") {
		d = strings.TrimSpace(d)
		switch {
		case d == "!postgresql":
			return false
		case d == "postgresql" || d == "all":
			included = true
		case !strings.HasPrefix(d, "!"):
			onlyExclusions = false
		}
	}
	return included || onlyExclusions
}
