package javatime

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testdata/vectors.tsv: random and mutated date strings parsed by Java 21.0.12 in
// America/Sao_Paulo with DateUtil.parseDate (I) and DateUtil.parse with
// "yyyy-MM-dd HH:mm:ss" (L): input and epoch milliseconds, or ERR where Java throws.
// JAVATIME_VECTORS names a larger file of the same format.
func TestJavaVectors(t *testing.T) {
	check(t, "testdata/vectors.tsv")
	if path := os.Getenv("JAVATIME_VECTORS"); path != "" {
		check(t, path)
	}
}

func check(t *testing.T, path string) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	n, failures := 0, 0
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		var got time.Time
		if fields[0] == "I" {
			got, err = ParseISOZoned(fields[1])
		} else {
			got, err = ParseLocal(fields[1], zone)
		}
		result := "ERR"
		if err == nil {
			result = strconv.FormatInt(got.UnixMilli(), 10)
		}
		n++
		if result != fields[2] {
			if failures++; failures <= 20 {
				t.Errorf("%s %q: got %s, Java %s", fields[0], fields[1], result, fields[2])
			}
		}
	}
	t.Logf("%s: %d inputs, %d mismatches", path, n, failures)
}
