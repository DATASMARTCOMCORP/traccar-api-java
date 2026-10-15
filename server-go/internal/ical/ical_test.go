package ical

import (
	"bufio"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testdata/vectors.tsv.gz holds random calendars and moments evaluated by Traccar's
// Calendar.checkMoment (ical4j 4.2.5) on Java 21.0.12 with the production JVM's defaults
// (America/Sao_Paulo, locale "en"): base64 iCalendar, epoch milliseconds, and true,
// false or ERR (Java threw). ICAL_VECTORS names a larger file of the same format.
func TestJavaVectors(t *testing.T) {
	check(t, "testdata/vectors.tsv.gz")
	if path := os.Getenv("ICAL_VECTORS"); path != "" {
		check(t, path)
	}
}

func check(t *testing.T, path string) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var in io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		in = zr
	}
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{DefaultZone: sp, FirstDayOfWeek: time.Sunday}
	cache := map[string]*Calendar{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n, failures := 0, 0
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		cal := cache[fields[0]]
		if cal == nil {
			data, err := base64.StdEncoding.DecodeString(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			if cal, err = Parse(data); err != nil {
				t.Fatalf("parse: %v\n%s", err, data)
			}
			cache[fields[0]] = cal
		}
		ms, _ := strconv.ParseInt(fields[1], 10, 64)
		in, err := cal.Contains(time.UnixMilli(ms), opts)
		got := strconv.FormatBool(in)
		if err != nil {
			got = "ERR"
		}
		n++
		if got != fields[2] {
			if failures++; failures <= 15 {
				data, _ := base64.StdEncoding.DecodeString(fields[0])
				t.Errorf("%s: got %s (%v), Java %s\n%s", time.UnixMilli(ms).UTC(), got, err, fields[2], data)
			}
		}
	}
	t.Logf("%s: %d checks, %d mismatches", path, n, failures)
}
