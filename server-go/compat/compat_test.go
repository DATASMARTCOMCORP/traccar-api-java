package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestDiffJSON(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		opts  jsonOptions
		diffs int
	}{
		{"key order and whitespace ignored", `{"a":1,"b":[1,2]}`, `{ "b": [1, 2], "a": 1 }`, jsonOptions{}, 0},
		{"int vs double is a difference", `{"speed":87}`, `{"speed":87.0}`, jsonOptions{}, 1},
		{"exponent spelling is a difference", `{"d":1.5E300}`, `{"d":1.5e300}`, jsonOptions{}, 1},
		{"missing key", `{"a":1,"b":2}`, `{"a":1}`, jsonOptions{}, 1},
		{"null vs absent", `{"a":null}`, `{}`, jsonOptions{}, 1},
		{"array order matters", `[1,2]`, `[2,1]`, jsonOptions{}, 2},
		{"geofenceIds are always unordered", `[{"geofenceIds":[2,1]}]`, `[{"geofenceIds":[1,2]}]`, jsonOptions{}, 0},
		{"geofenceIds still need the same ids", `[{"geofenceIds":[2,1]}]`, `[{"geofenceIds":[1,3]}]`, jsonOptions{}, 1},
		{"unordered array", `[{"t":"web"},{"t":"mail"}]`, `[{"t":"mail"},{"t":"web"}]`,
			jsonOptions{unordered: []string{"$"}}, 0},
		{"server clocks within tolerance", `{"t":"2026-10-14T19:11:35.225+00:00"}`,
			`{"t":"2026-10-14T19:11:37.001+00:00"}`, jsonOptions{}, 0},
		{"clock beyond tolerance", `{"t":"2026-10-14T19:11:35.225+00:00"}`,
			`{"t":"2026-10-14T22:11:35.225+00:00"}`, jsonOptions{}, 1},
		{"date format is checked", `{"t":"2026-10-14T19:11:35.225+00:00"}`,
			`{"t":"2026-10-14T19:11:35.225Z"}`, jsonOptions{}, 1},
		{"opaque by shape", `{"token":"abc"}`, `{"token":"xyz"}`, jsonOptions{opaque: []string{"$.token"}}, 0},
		{"opaque still needs same shape", `{"token":"abc"}`, `{"token":""}`, jsonOptions{opaque: []string{"$.token"}}, 1},
		{"opaque with index wildcard", `[{"k":"a"},{"k":"b"}]`, `[{"k":"c"},{"k":"d"}]`,
			jsonOptions{opaque: []string{"$[*].k"}}, 0},
		{"aliases line up random ids", `{"objectId":111}`, `{"objectId":222}`,
			jsonOptions{aliasA: map[string]string{"111": "<token 1>"}, aliasB: map[string]string{"222": "<token 1>"}}, 0},
		{"aliases catch the wrong id", `{"objectId":111}`, `{"objectId":333}`,
			jsonOptions{aliasA: map[string]string{"111": "<token 1>"}, aliasB: map[string]string{"222": "<token 1>"}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffs, err := diffJSON([]byte(tt.a), []byte(tt.b), tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(diffs) != tt.diffs {
				t.Errorf("got %d differences, want %d: %q", len(diffs), tt.diffs, diffs)
			}
		})
	}
}

func TestDiffJSONRejectsTrailingData(t *testing.T) {
	if _, err := diffJSON([]byte(`{} {}`), []byte(`{}`), jsonOptions{}); err == nil {
		t.Error("want error for trailing data")
	}
}

func ptr(s string) *string { return &s }

func TestSameValue(t *testing.T) {
	tests := []struct {
		rule valueRule
		a, b *string
		want bool
	}{
		{ruleExact, ptr("87.0"), ptr("87.0"), true},
		{ruleExact, ptr("87.0"), ptr("87"), false},
		{ruleExact, nil, ptr(""), false},
		{ruleExact, nil, nil, true},
		{ruleClock, ptr("2026-10-14 16:30:09.497"), ptr("2026-10-14 16:30:11.2"), true},
		{ruleClock, ptr("2026-10-14 13:30:09.497"), ptr("2026-10-14 16:30:09.497"), false},
		{ruleClock, ptr("2026-10-14 16:30:09"), nil, false},
		{ruleHex48, ptr(strings.Repeat("ab", 24)), ptr(strings.Repeat("0f", 24)), true},
		{ruleHex48, ptr(strings.Repeat("ab", 24)), ptr("short"), false},
		{ruleBytes, ptr(`\x3059`), ptr(`\x30aa`), true},
		{ruleBytes, ptr(`\x3059`), ptr(``), false},
		{ruleJSONSet, ptr(`[2,1]`), ptr(`[1,2]`), true},
		{ruleJSONSet, ptr(`[2,1]`), ptr(`[1,3]`), false},
		{ruleJSONSet, ptr(`[1,2]`), ptr(`[1, 2]`), false},
		{ruleJSONSet, ptr(`[1.0,2]`), ptr(`[2,1]`), false},
		{ruleJSONSet, ptr(`null`), ptr(`[]`), false},
		{ruleJSONSet, ptr(`null`), ptr(`null`), true},
	}
	for _, tt := range tests {
		if got := sameValue(tt.rule, tt.a, tt.b); got != tt.want {
			t.Errorf("sameValue(%v, %s, %s) = %v, want %v", tt.rule, quote(tt.a), quote(tt.b), got, tt.want)
		}
	}
}

func TestDiffSnapshots(t *testing.T) {
	cols := []column{{"id", "integer"}, {"attributes", "character varying"}, {"servertime", "timestamp without time zone"}}
	a := snapshot{"tc_positions": {columns: cols, key: []int{0}, rows: [][]*string{
		{ptr("1"), ptr(`{"a":87.0}`), ptr("2026-10-14 16:30:09.497")},
		{ptr("2"), ptr(`{}`), ptr("2026-10-14 16:30:10")},
	}}}
	b := snapshot{"tc_positions": {columns: cols, key: []int{0}, rows: [][]*string{
		{ptr("1"), ptr(`{"a":87}`), ptr("2026-10-14 16:30:09.9")},
		{ptr("3"), ptr(`{}`), ptr("2026-10-14 16:30:10")},
	}}}
	diffs := diffSnapshots(a, b)
	want := []string{
		`tc_positions[1].attributes: A="{\"a\":87.0}" B="{\"a\":87}"`,
		`tc_positions: row only in A: id="2" attributes="{}" servertime="2026-10-14 16:30:10"`,
		`tc_positions: row only in B: id="3" attributes="{}" servertime="2026-10-14 16:30:10"`,
	}
	if strings.Join(diffs, "\n") != strings.Join(want, "\n") {
		t.Errorf("diffs:\n%s\nwant:\n%s", strings.Join(diffs, "\n"), strings.Join(want, "\n"))
	}
}

func TestSnapshotAlias(t *testing.T) {
	cols := []column{{"id", "bigint"}}
	a := snapshot{"tc_revoked_tokens": {columns: cols, key: []int{0}, rows: [][]*string{{ptr("270238445592260404")}}}}
	b := snapshot{"tc_revoked_tokens": {columns: cols, key: []int{0}, rows: [][]*string{{ptr("4676191008842512671")}}}}
	a.alias(map[string]string{"270238445592260404": "<token 1>"})
	b.alias(map[string]string{"4676191008842512671": "<token 1>"})
	if d := diffSnapshots(a, b); len(d) != 0 {
		t.Errorf("aliased snapshots differ: %q", d)
	}
}

func TestDiffCSV(t *testing.T) {
	a := "id,serverTime,hdop,batteryLevel\n1,2026-10-14 16:42:55,1.5,87.0\n"
	b := "id,serverTime,batteryLevel,hdop\n1,2026-10-14 16:42:56,87.0,1.5\n"
	if d := diffCSV([]byte(a), []byte(b)); len(d) != 0 {
		t.Errorf("reordered columns differ: %q", d)
	}
	c := "id,serverTime,batteryLevel,hdop\n1,2026-10-14 16:42:56,87,1.5\n"
	if d := diffCSV([]byte(a), []byte(c)); len(d) != 1 {
		t.Errorf("want one difference, got %q", d)
	}
}

func TestTokenID(t *testing.T) {
	sig := []byte{0x30, 0x02, 0x01, 0x01}
	payload := []byte(`{"i":4676191008842512671,"u":1,"e":"2035-01-01T00:00:00.000+00:00"}`)
	raw := append(append([]byte{byte(len(sig))}, sig...), payload...)
	id, err := tokenID(base64.RawURLEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	if id != "4676191008842512671" {
		t.Errorf("id = %s", id) // must not lose precision through float64
	}
	if _, err := tokenID("not a token"); err == nil {
		t.Error("want error for malformed token")
	}
}

func TestAtrackText(t *testing.T) {
	got := string(atrackText("@P,CA4B,0,1,358683064932578,1533633976,%CI%MV%BV,143,0\r\n"))
	want := "@P,CA4B,46,1,358683064932578,1533633976,%CI%MV%BV,143,0\r\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// AtrackFrameDecoder: frame length = index of the comma after the field + the field
	// value, plus the trailing '\n'.
	lengthEnd := strings.Index(got[len("@P,CA4B,"):], ",") + len("@P,CA4B,")
	if lengthEnd+46+1 != len(got) {
		t.Errorf("frame of %d bytes doesn't match length field 46 (field ends at %d)", len(got), lengthEnd)
	}
}

func TestCookies(t *testing.T) {
	a := http.Header{"Set-Cookie": {"JSESSIONID=node01abc.node0; Path=/; HttpOnly"}}
	b := http.Header{"Set-Cookie": {"JSESSIONID=node0xyz.node0; Path=/; HttpOnly"}}
	c := http.Header{"Set-Cookie": {"JSESSIONID=node0xyz.node0; Path=/"}}
	if cookies(a) != cookies(b) {
		t.Errorf("cookie values should be ignored: %q vs %q", cookies(a), cookies(b))
	}
	if cookies(a) == cookies(c) {
		t.Error("cookie attributes must be compared")
	}
}

func TestExpand(t *testing.T) {
	s := &side{vars: map[string]string{"id": "42"}}
	if got, err := s.expand(`{"id":${id}}`); err != nil || got != `{"id":42}` {
		t.Errorf("expand = %q, %v", got, err)
	}
	if _, err := s.expand("${missing}"); err == nil {
		t.Error("want error for undefined variable")
	}
}
