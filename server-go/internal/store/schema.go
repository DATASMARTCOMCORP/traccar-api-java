package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// changeset identifies a Liquibase changeset the way databasechangelog does.
type changeset struct {
	id, author, file string
}

type knownChangeset struct {
	changeset
	// onPostgres is false for changesets restricted to another DBMS; Liquibase skips them
	// without recording a row.
	onPostgres bool
	// optional changesets may or may not be applied; the Go server works either way.
	optional bool
}

// knownChangesets is the Liquibase history Go is written against: schema/changelog-*.xml
// at Traccar 6.14.5 plus the SIR changeset. TestKnownChangesetsMatchChangelogs keeps it
// in sync with the XML. When Java's changelog gains a changeset, Go refuses to start
// until the change is reviewed and added here.
var knownChangesets = []knownChangeset{
	{changeset{"changelog-4.0-clean", "author", "changelog-4.0-clean"}, true, false},
	{changeset{"changelog-4.0-clean-common", "author", "changelog-4.0-clean"}, true, false},
	{changeset{"changelog-4.1-mssql", "author", "changelog-4.1"}, true, false}, // MARK_RAN on Postgres
	{changeset{"changelog-4.7", "author", "changelog-4.7"}, true, false},
	{changeset{"changelog-4.9", "author", "changelog-4.9"}, true, false},
	{changeset{"changelog-4.10", "author", "changelog-4.10"}, true, false},
	{changeset{"changelog-4.11", "author", "changelog-4.11"}, true, false},
	{changeset{"changelog-4.13", "author", "changelog-4.13"}, true, false},
	{changeset{"changelog-4.15", "author", "changelog-4.15"}, true, false},
	{changeset{"changelog-5.0", "author", "changelog-5.0"}, true, false},
	{changeset{"changelog-5.1", "author", "changelog-5.1"}, true, false},
	{changeset{"changelog-5.2", "author", "changelog-5.2"}, true, false},
	{changeset{"changelog-5.3", "author", "changelog-5.3"}, true, false},
	{changeset{"changelog-5.4", "author", "changelog-5.4"}, true, false},
	{changeset{"changelog-5.5", "author", "changelog-5.5"}, true, false},
	{changeset{"changelog-5.6", "author", "changelog-5.6"}, true, false},
	{changeset{"changelog-5.7", "author", "changelog-5.7"}, true, false},
	{changeset{"changelog-5.8", "author", "changelog-5.8"}, true, false},
	{changeset{"changelog-5.9", "author", "changelog-5.9"}, true, false},
	{changeset{"changelog-5.10", "author", "changelog-5.10"}, true, false},
	{changeset{"changelog-5.11", "author", "changelog-5.11"}, true, false},
	{changeset{"changelog-6.2", "author", "changelog-6.2"}, true, false},
	{changeset{"changelog-6.3-old", "author", "changelog-6.3"}, true, false},
	{changeset{"changelog-6.3-new", "author", "changelog-6.3"}, true, false},
	{changeset{"changelog-6.6", "author", "changelog-6.6"}, true, false},
	{changeset{"changelog-6.7.0", "author", "changelog-6.7.0"}, true, false},
	{changeset{"changelog-6.8.0-timescale", "author", "changelog-6.8.0"}, true, false},
	{changeset{"changelog-6.9.0", "author", "changelog-6.9.0"}, true, false},
	{changeset{"changelog-6.11.0-timescale", "author", "changelog-6.11.0"}, true, false},
	{changeset{"changelog-6.11.0-other", "author", "changelog-6.11.0"}, true, false},
	{changeset{"changelog-6.11.0", "author", "changelog-6.11.0"}, true, false},
	{changeset{"changelog-6.13.0", "author", "changelog-6.13.0"}, true, false},
	// Optional: production's history (2026-10-15) doesn't have it; see legacyChangesets.
	{changeset{"changelog-6.13.0-fk-linkeddeviceid", "author", "changelog-6.13.0"}, true, true},
	{changeset{"changelog-6.13.0-fk-linkeddeviceid-mssql", "author", "changelog-6.13.0"}, false, false},
	// Optional for the same reason; without it tc_devices.positionid and other ids stay
	// integer, which the Go code reads into int64 either way.
	{changeset{"changelog-6.15.0", "author", "changelog-6.15.0"}, true, true},
	// SIR: tc_geofences.area as TEXT. Its include was dropped from changelog-master.xml by
	// the upstream merge 907139a13, so databases may or may not have it (MIGRATION.md §8).
	{changeset{"changelog-6.13-dsc", "dsc-trackhub", "changelog-6.13-dsc"}, true, true},
}

// legacyChangesets are rows that older Traccar changelogs (3.x and the old 4.0 upgrade
// path) recorded, all MARK_RAN on Postgres. They are in production's history
// (testdata/production-changelog.tsv) but no longer in schema/, so they are accepted,
// never required.
var legacyChangesets = []changeset{
	{"changelog-3.3", "author", "changelog-3.3"},
	{"changelog-3.5", "author", "changelog-3.5"},
	{"changelog-3.6", "author", "changelog-3.6"},
	{"changelog-3.7", "author", "changelog-3.7"},
	{"changelog-3.7-notmssql", "author", "changelog-3.7"},
	{"changelog-3.8", "author", "changelog-3.8"},
	{"changelog-3.9", "author", "changelog-3.9"},
	{"changelog-3.10", "author", "changelog-3.10"},
	{"changelog-3.10-notmssql", "author", "changelog-3.10"},
	{"changelog-3.10-mssql", "author", "changelog-3.10"},
	{"changelog-3.7-mssql", "author", "changelog-3.10"},
	{"changelog-3.11", "author", "changelog-3.11"},
	{"changelog-3.12", "author", "changelog-3.12"},
	{"changelog-3.12-notmssql", "author", "changelog-3.12"},
	{"changelog-3.12-pgsql", "author", "changelog-3.12"},
	{"changelog-3.14", "author", "changelog-3.14"},
	{"changelog-3.15", "author", "changelog-3.15"},
	{"changelog-3.16", "author", "changelog-3.16"},
	{"changelog-3.3-admin", "author", "changelog-3.17"},
	{"changelog-3.17", "author", "changelog-3.17"},
	{"changelog-4.0-pre", "author", "changelog-4.0"},
	{"changelog-4.0-common", "author", "changelog-4.0"},
	{"changelog-4.0-pg", "author", "changelog-4.0"},
	{"changelog-4.0", "author", "changelog-4.0"},
	{"changelog-4.0-renaming", "author", "changelog-4.0"},
}

// ErrSchema is returned (wrapped) when the database isn't at a schema version Go knows.
var ErrSchema = errors.New("unsupported database schema")

// CheckSchema compares databasechangelog with knownChangesets. It fails when Liquibase
// holds its lock (Java is migrating), when a changeset Go doesn't know was applied
// (newer schema), or when one Go needs is missing (older schema). Checksums are not
// compared: Liquibase rewrites them across its own versions.
func (s *Store) CheckSchema(ctx context.Context) error {
	var locked bool
	err := s.pool.QueryRow(ctx, "SELECT locked FROM databasechangeloglock WHERE id = 1").Scan(&locked)
	if err != nil {
		return fmt.Errorf("%w: reading databasechangeloglock (has Java created the schema?): %v", ErrSchema, err)
	}
	if locked {
		return fmt.Errorf("%w: Liquibase lock is held; Java is migrating the database", ErrSchema)
	}

	rows, err := s.pool.Query(ctx, "SELECT id, author, filename FROM databasechangelog ORDER BY orderexecuted")
	if err != nil {
		return fmt.Errorf("%w: reading databasechangelog: %v", ErrSchema, err)
	}
	defer rows.Close()
	var applied []changeset
	for rows.Next() {
		var c changeset
		if err := rows.Scan(&c.id, &c.author, &c.file); err != nil {
			return err
		}
		applied = append(applied, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return checkChangesets(applied)
}

func checkChangesets(applied []changeset) error {
	known := map[changeset]knownChangeset{}
	for _, k := range knownChangesets {
		known[k.changeset] = k
	}
	for _, c := range legacyChangesets {
		known[c] = knownChangeset{changeset: c, onPostgres: true, optional: true}
	}
	have := map[changeset]bool{}
	var unknown, missing []string
	for _, c := range applied {
		have[c] = true
		if _, ok := known[c]; !ok {
			unknown = append(unknown, c.file+"::"+c.id+"::"+c.author)
		}
	}
	for _, k := range knownChangesets {
		if k.onPostgres && !k.optional && !have[k.changeset] {
			missing = append(missing, k.file+"::"+k.id+"::"+k.author)
		}
	}
	var problems []string
	if len(unknown) > 0 {
		problems = append(problems, "applied changesets unknown to this build (schema is newer): "+strings.Join(unknown, ", "))
	}
	if len(missing) > 0 {
		problems = append(problems, "changesets not applied (schema is older; run the Java server to migrate): "+strings.Join(missing, ", "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrSchema, strings.Join(problems, "; "))
	}
	return nil
}
