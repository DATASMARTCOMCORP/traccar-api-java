package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"slices"
	"time"
)

// diffCSV compares two CSV exports by column name. Java builds the attribute columns
// from a HashSet, so their order changes between JVM runs; rows keep their order.
// Cells in the server's "2006-01-02 15:04:05" format may differ by clockTolerance.
func diffCSV(a, b []byte) []string {
	ra, errA := csv.NewReader(bytes.NewReader(a)).ReadAll()
	rb, errB := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if errA != nil || errB != nil {
		return []string{fmt.Sprintf("csv: A=%v B=%v", errA, errB)}
	}
	if len(ra) == 0 || len(rb) == 0 {
		if len(ra) != len(rb) {
			return []string{fmt.Sprintf("csv: rows A=%d B=%d", len(ra), len(rb))}
		}
		return nil
	}
	ha, hb := slices.Sorted(slices.Values(ra[0])), slices.Sorted(slices.Values(rb[0]))
	if !slices.Equal(ha, hb) {
		return []string{fmt.Sprintf("csv header: A=%q B=%q", ra[0], rb[0])}
	}
	var out []string
	if len(ra) != len(rb) {
		out = append(out, fmt.Sprintf("csv: rows A=%d B=%d", len(ra), len(rb)))
	}
	for i := 1; i < min(len(ra), len(rb)); i++ {
		for ci, name := range ra[0] {
			cj := slices.Index(rb[0], name)
			va, vb := cell(ra[i], ci), cell(rb[i], cj)
			if va != vb && !clockClose(va, vb) {
				out = append(out, fmt.Sprintf("csv row %d %s: A=%q B=%q", i, name, va, vb))
			}
		}
	}
	return out
}

func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

func clockClose(a, b string) bool {
	const layout = "2006-01-02 15:04:05"
	ta, errA := time.Parse(layout, a)
	tb, errB := time.Parse(layout, b)
	return errA == nil && errB == nil && ta.Sub(tb).Abs() <= clockTolerance
}
