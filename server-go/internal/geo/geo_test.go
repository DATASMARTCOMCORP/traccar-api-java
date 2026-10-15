package geo

import (
	"bufio"
	"encoding/hex"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// testdata/vectors.tsv comes from Java 21.0.12 with Traccar's classes:
//
//	N  input  Double.parseDouble bits  Integer.parseInt  Long.parseLong   (or ERR)
//	G  area  polylineDistance  lat bits  lon bits  altitude  floor  ceiling  containsPosition
//
// GEO_VECTORS names a larger file of the same format.
func TestJavaVectors(t *testing.T) {
	check(t, "testdata/vectors.tsv")
	if path := os.Getenv("GEO_VECTORS"); path != "" {
		check(t, path)
	}
}

func check(t *testing.T, path string) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n, failures := 0, 0
	fail := func(format string, args ...any) {
		if failures++; failures <= 20 {
			t.Errorf(format, args...)
		}
	}
	bits := func(s string) float64 {
		u, err := strconv.ParseUint(s, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		return math.Float64frombits(u)
	}
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		n++
		switch f[0] {
		case "N":
			raw, err := hex.DecodeString(f[1])
			if err != nil {
				t.Fatal(err)
			}
			f[1] = string(raw)
			d := "ERR"
			if v, err := model.ParseDouble(f[1]); err == nil {
				d = strconv.FormatUint(math.Float64bits(v), 16)
			}
			i := "ERR"
			if v, err := model.ParseInt(f[1]); err == nil {
				i = strconv.FormatInt(int64(v), 10)
			}
			l := "ERR"
			if v, err := model.ParseLong(f[1]); err == nil {
				l = strconv.FormatInt(v, 10)
			}
			if d != f[2] || i != f[3] || l != f[4] {
				fail("numbers %q: got %s %s %s, Java %s %s %s", f[1], d, i, l, f[2], f[3], f[4])
			}
		case "G":
			g := model.Geofence{Area: f[1]}
			if pd, _ := strconv.ParseFloat(f[2], 64); pd != 25 {
				g.Attributes.Set("polylineDistance", pd)
			}
			if f[6] != "" {
				g.Attributes.Set("floor", f[6])
			}
			if f[7] != "" {
				g.Attributes.Set("ceiling", f[7])
			}
			alt, _ := strconv.ParseFloat(f[5], 64)
			in, err := GeofenceContains(&g, bits(f[3]), bits(f[4]), alt)
			got := strconv.FormatBool(in)
			if err != nil {
				got = "ERR"
			}
			if got != f[8] {
				fail("%s at %v %v alt %v (floor %q ceiling %q): got %s (%v), Java %s", f[1], bits(f[3]), bits(f[4]), alt, f[6], f[7], got, err, f[8])
			}
		}
	}
	t.Logf("%s: %d vectors, %d mismatches", path, n, failures)
}
