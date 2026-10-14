package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// A snapshot holds every row of every table in the public schema, with each value as
// Postgres text (col::text). Comparing text, not decoded values, is what "100% DB
// compatible" means here: the same row must be stored with the same text.
type snapshot map[string]*table

type table struct {
	columns []column
	key     []int // indexes of primary-key columns; empty means compare rows as a multiset
	rows    [][]*string
}

type column struct {
	name     string
	dataType string
}

func takeSnapshot(ctx context.Context, conn *pgx.Conn) (snapshot, error) {
	rows, err := conn.Query(ctx, `
		SELECT c.table_name, c.column_name, c.data_type
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	snap := snapshot{}
	for rows.Next() {
		var tbl string
		var col column
		if err := rows.Scan(&tbl, &col.name, &col.dataType); err != nil {
			return nil, err
		}
		if snap[tbl] == nil {
			snap[tbl] = &table{}
		}
		snap[tbl].columns = append(snap[tbl].columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	keys, err := conn.Query(ctx, `
		SELECT tc.table_name, kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
		WHERE tc.table_schema = 'public' AND tc.constraint_type = 'PRIMARY KEY'
		ORDER BY tc.table_name, kcu.ordinal_position`)
	if err != nil {
		return nil, fmt.Errorf("list primary keys: %w", err)
	}
	for keys.Next() {
		var tbl, col string
		if err := keys.Scan(&tbl, &col); err != nil {
			return nil, err
		}
		if t := snap[tbl]; t != nil {
			t.key = append(t.key, slices.IndexFunc(t.columns, func(c column) bool { return c.name == col }))
		}
	}
	if err := keys.Err(); err != nil {
		return nil, err
	}

	for name, t := range snap {
		exprs := make([]string, len(t.columns))
		for i, c := range t.columns {
			exprs[i] = pgx.Identifier{c.name}.Sanitize() + "::text"
		}
		q := "SELECT " + strings.Join(exprs, ", ") + " FROM " + pgx.Identifier{name}.Sanitize()
		data, err := conn.Query(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		for data.Next() {
			row := make([]*string, len(t.columns))
			ptrs := make([]any, len(row))
			for i := range row {
				ptrs[i] = &row[i]
			}
			if err := data.Scan(ptrs...); err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			t.rows = append(t.rows, row)
		}
		if err := data.Err(); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		slices.SortFunc(t.rows, func(x, y []*string) int { return strings.Compare(t.rowKey(x), t.rowKey(y)) })
	}
	return snap, nil
}

// rowKey identifies a row for alignment: its primary key, or the whole row without one.
func (t *table) rowKey(row []*string) string {
	idx := t.key
	if len(idx) == 0 {
		idx = make([]int, len(row))
		for i := range idx {
			idx[i] = i
		}
	}
	parts := make([]string, len(idx))
	for i, c := range idx {
		parts[i] = nullText(row[c])
	}
	// Zero-pad numeric keys so ids sort numerically in reports.
	for i, p := range parts {
		if digits.MatchString(p) {
			parts[i] = fmt.Sprintf("%020s", p)
		}
	}
	return strings.Join(parts, "\x1f")
}

var digits = regexp.MustCompile(`^\d+$`)

func nullText(v *string) string {
	if v == nil {
		return "NULL"
	}
	return *v
}

// alias replaces values that are random by design with their stable alias (see
// jsonOptions), then re-sorts rows since aliased values may be part of a key.
func (s snapshot) alias(aliases map[string]string) {
	if len(aliases) == 0 {
		return
	}
	for _, t := range s {
		for _, row := range t.rows {
			for i, v := range row {
				if v == nil {
					continue
				}
				if a, ok := aliases[*v]; ok {
					row[i] = &a
				}
			}
		}
		slices.SortFunc(t.rows, func(x, y []*string) int { return strings.Compare(t.rowKey(x), t.rowKey(y)) })
	}
}

// fingerprint changes whenever any row changes; used to wait until a server has
// finished its asynchronous writes.
func (s snapshot) fingerprint() string {
	h := sha256.New()
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		fmt.Fprintf(h, "%s\x1e", n)
		for _, r := range s[n].rows {
			for _, v := range r {
				fmt.Fprintf(h, "%s\x1f", nullText(v))
			}
			h.Write([]byte{'\x1e'})
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// valueRule says how one column is compared. Everything not listed is compared exactly.
type valueRule int

const (
	ruleExact valueRule = iota
	// ruleClock: a timestamp each server takes from its own clock (servertime,
	// lastupdate, actiontime...). Equal within clockTolerance. A zone or encoding bug
	// shifts the value by hours or changes its format, so it is still caught.
	ruleClock
	// ruleHex48: random salt or PBKDF2 hash; both must be 48 lowercase hex chars.
	// Step 3 cross-checks the hashes by verifying passwords.
	ruleHex48
	// ruleBytes: a random key (tc_keystore); both must be non-empty bytea.
	ruleBytes
	// ruleJSONSet: a JSON array whose order Java doesn't define (geofenceids comes from
	// a HashSet, so two JVM runs store [1,2] and [2,1]). Same elements, any order.
	ruleJSONSet
)

func ruleFor(tbl string, c column) valueRule {
	switch tbl + "." + c.name {
	case "tc_users.hashedpassword", "tc_users.salt":
		return ruleHex48
	case "tc_keystore.publickey", "tc_keystore.privatekey":
		return ruleBytes
	case "tc_positions.geofenceids":
		return ruleJSONSet
	}
	if c.dataType == "timestamp without time zone" {
		return ruleClock
	}
	return ruleExact
}

var (
	hex48   = regexp.MustCompile(`^[0-9a-f]{48}$`)
	byteaRe = regexp.MustCompile(`^\\x([0-9a-f]{2})+$`)
)

func sameValue(rule valueRule, a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	if *a == *b {
		return true
	}
	switch rule {
	case ruleClock:
		ta, errA := time.Parse("2006-01-02 15:04:05.999999", *a)
		tb, errB := time.Parse("2006-01-02 15:04:05.999999", *b)
		return errA == nil && errB == nil && ta.Sub(tb).Abs() <= clockTolerance
	case ruleHex48:
		return hex48.MatchString(*a) && hex48.MatchString(*b)
	case ruleBytes:
		return byteaRe.MatchString(*a) && byteaRe.MatchString(*b)
	case ruleJSONSet:
		va, errA := decodeJSON([]byte(*a))
		vb, errB := decodeJSON([]byte(*b))
		la, okA := va.([]any)
		lb, okB := vb.([]any)
		if errA != nil || errB != nil || !okA || !okB || len(la) != len(lb) {
			return false
		}
		// Same element texts in any order; the text form ([1,2]) is still checked by
		// requiring both sides to be arrays of the same literals.
		return slices.Equal(sortedTexts(la), sortedTexts(lb)) && sameSpacing(*a, *b)
	}
	return false
}

func sortedTexts(v []any) []string {
	out := make([]string, len(v))
	for i, e := range v {
		out[i] = canonical(e)
	}
	slices.Sort(out)
	return out
}

// sameSpacing checks that two JSON texts use the same separators and whitespace, which
// reordering elements doesn't change.
func sameSpacing(a, b string) bool {
	strip := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ',' || r == '[' || r == ']' || r == ' ' {
				return r
			}
			return -1
		}, s)
	}
	return strip(a) == strip(b)
}

func diffSnapshots(a, b snapshot) []string {
	var out []string
	names := make([]string, 0, len(a)+len(b))
	for n := range a {
		names = append(names, n)
	}
	for n := range b {
		if a[n] == nil {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		ta, tb := a[n], b[n]
		switch {
		case ta == nil:
			out = append(out, fmt.Sprintf("table %s: only in B", n))
			continue
		case tb == nil:
			out = append(out, fmt.Sprintf("table %s: only in A", n))
			continue
		}
		if !slices.Equal(ta.columns, tb.columns) {
			out = append(out, fmt.Sprintf("table %s: columns differ: A=%v B=%v", n, ta.columns, tb.columns))
			continue
		}
		out = append(out, diffTable(n, ta, tb)...)
	}
	return out
}

func diffTable(name string, a, b *table) []string {
	var out []string
	i, j := 0, 0
	for i < len(a.rows) || j < len(b.rows) {
		var ka, kb string
		if i < len(a.rows) {
			ka = a.rowKey(a.rows[i])
		}
		if j < len(b.rows) {
			kb = b.rowKey(b.rows[j])
		}
		switch {
		case j >= len(b.rows) || (i < len(a.rows) && ka < kb):
			out = append(out, fmt.Sprintf("%s: row only in A: %s", name, a.format(a.rows[i])))
			i++
		case i >= len(a.rows) || kb < ka:
			out = append(out, fmt.Sprintf("%s: row only in B: %s", name, b.format(b.rows[j])))
			j++
		default:
			for c, col := range a.columns {
				if !sameValue(ruleFor(name, col), a.rows[i][c], b.rows[j][c]) {
					out = append(out, fmt.Sprintf("%s[%s].%s: A=%s B=%s",
						name, strings.ReplaceAll(strings.TrimLeft(ka, "0"), "\x1f", ","), col.name,
						quote(a.rows[i][c]), quote(b.rows[j][c])))
				}
			}
			i++
			j++
		}
	}
	return out
}

func (t *table) format(row []*string) string {
	parts := make([]string, len(row))
	for i, v := range row {
		parts[i] = t.columns[i].name + "=" + quote(v)
	}
	s := strings.Join(parts, " ")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func quote(v *string) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%q", *v)
}

// waitQuiet takes snapshots until two in a row are identical, so asynchronous position
// processing has finished before rows are compared.
func waitQuiet(ctx context.Context, conn *pgx.Conn) (snapshot, error) {
	var last string
	deadline := time.Now().Add(30 * time.Second)
	for {
		snap, err := takeSnapshot(ctx, conn)
		if err != nil {
			return nil, err
		}
		fp := snap.fingerprint()
		if fp == last {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return snap, fmt.Errorf("database still changing after 30s")
		}
		last = fp
		time.Sleep(400 * time.Millisecond)
	}
}
