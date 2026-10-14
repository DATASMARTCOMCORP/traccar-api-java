package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Semantic JSON comparison.
//
// "Semantic" means object key order and whitespace don't matter. Everything else is
// strict on purpose:
//   - Numbers are compared by their literal text, because Java keeps the int/double
//     distinction (87 vs 87.0) and clients see it.
//   - Array order matters, because Java returns rows in a defined order.
//   - Strings must be equal, with two exceptions. Strings in Java's date format
//     (2026-10-14T19:11:35.225+00:00) may differ by up to clockTolerance, since each
//     server stamps its own "now". Paths listed as opaque only need the same shape
//     (same JSON type, and strings both empty or both non-empty), for values that are
//     random by design such as tokens.
//   - Paths listed as unordered compare an array as a multiset, for Java collections
//     whose iteration order changes from one JVM to the next (HashSet). Every
//     "geofenceIds" array is unordered: Java fills it from a HashSet of geofences.
//   - Values that are random by design but meaningful (token ids) are first replaced by
//     per-side aliases. "<token 1>" on A must line up with "<token 1>" on B, so the
//     harness still checks that the right token was revoked.

const clockTolerance = 5 * time.Second

var javaDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}\+00:00$`)

type jsonOptions struct {
	opaque, unordered []string
	aliasA, aliasB    map[string]string
}

func diffJSON(a, b []byte, opts jsonOptions) ([]string, error) {
	va, err := decodeJSON(a)
	if err != nil {
		return nil, fmt.Errorf("side A: %w", err)
	}
	vb, err := decodeJSON(b)
	if err != nil {
		return nil, fmt.Errorf("side B: %w", err)
	}
	c := comparer{
		opaque:    patterns(opts.opaque),
		unordered: patterns(opts.unordered),
	}
	var out []string
	c.compare("$", alias(va, opts.aliasA), alias(vb, opts.aliasB), &out)
	return out, nil
}

type comparer struct {
	opaque, unordered []*regexp.Regexp
}

func patterns(paths []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(paths))
	for i, p := range paths {
		out[i] = pathPattern(p)
	}
	return out
}

// alias replaces scalar values that are keys of aliases (as text) with their alias.
func alias(v any, aliases map[string]string) any {
	if len(aliases) == 0 {
		return v
	}
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = alias(e, aliases)
		}
	case []any:
		for i, e := range x {
			x[i] = alias(e, aliases)
		}
	case string:
		if a, ok := aliases[x]; ok {
			return a
		}
	case json.Number:
		if a, ok := aliases[string(x)]; ok {
			return a
		}
	}
	return v
}

func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

// pathPattern turns "$[*].token" into a regexp; "[*]" matches any array index.
func pathPattern(p string) *regexp.Regexp {
	q := regexp.QuoteMeta(p)
	q = strings.ReplaceAll(q, `\[\*\]`, `\[\d+\]`)
	return regexp.MustCompile("^" + q + "$")
}

func matchesAny(path string, patterns []*regexp.Regexp) bool {
	for _, p := range patterns {
		if p.MatchString(path) {
			return true
		}
	}
	return false
}

func (c comparer) compare(path string, a, b any, out *[]string) {
	if matchesAny(path, c.opaque) {
		if !sameShape(a, b) {
			*out = append(*out, fmt.Sprintf("%s: shape differs: A=%s B=%s", path, render(a), render(b)))
		}
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			break
		}
		keys := make([]string, 0, len(av)+len(bv))
		for k := range av {
			keys = append(keys, k)
		}
		for k := range bv {
			if _, ok := av[k]; !ok {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			x, inA := av[k]
			y, inB := bv[k]
			child := path + "." + k
			switch {
			case !inA:
				*out = append(*out, fmt.Sprintf("%s: only in B: %s", child, render(y)))
			case !inB:
				*out = append(*out, fmt.Sprintf("%s: only in A: %s", child, render(x)))
			default:
				c.compare(child, x, y, out)
			}
		}
		return
	case []any:
		bv, ok := b.([]any)
		if !ok {
			break
		}
		if len(av) != len(bv) {
			*out = append(*out, fmt.Sprintf("%s: array length A=%d B=%d", path, len(av), len(bv)))
		}
		if matchesAny(path, c.unordered) || strings.HasSuffix(path, ".geofenceIds") {
			av, bv = sortedCopy(av), sortedCopy(bv)
		}
		for i := range min(len(av), len(bv)) {
			c.compare(path+"["+strconv.Itoa(i)+"]", av[i], bv[i], out)
		}
		return
	case string:
		if bv, ok := b.(string); ok {
			if av == bv || datesClose(av, bv) {
				return
			}
		}
	case json.Number:
		if bv, ok := b.(json.Number); ok && av == bv {
			return
		}
	default: // bool, nil
		if a == b {
			return
		}
	}
	*out = append(*out, fmt.Sprintf("%s: A=%s B=%s", path, render(a), render(b)))
}

// sortedCopy orders elements by their canonical JSON (json.Marshal sorts map keys).
func sortedCopy(v []any) []any {
	out := slices.Clone(v)
	slices.SortStableFunc(out, func(x, y any) int { return strings.Compare(canonical(x), canonical(y)) })
	return out
}

func canonical(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func datesClose(a, b string) bool {
	if !javaDate.MatchString(a) || !javaDate.MatchString(b) {
		return false
	}
	const layout = "2006-01-02T15:04:05.000-07:00"
	ta, errA := time.Parse(layout, a)
	tb, errB := time.Parse(layout, b)
	if errA != nil || errB != nil {
		return false
	}
	return ta.Sub(tb).Abs() <= clockTolerance
}

func sameShape(a, b any) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && (av == "") == (bv == "")
	case json.Number:
		_, ok := b.(json.Number)
		return ok
	case bool:
		_, ok := b.(bool)
		return ok
	case nil:
		return b == nil
	case []any:
		_, ok := b.([]any)
		return ok
	case map[string]any:
		_, ok := b.(map[string]any)
		return ok
	}
	return false
}

func render(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	const limit = 200
	if len(data) > limit {
		return string(data[:limit]) + "…"
	}
	return string(data)
}
