package geo

import (
	"bufio"
	"compress/gzip"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestPlusCodeJava compares with codes from openlocationcode 1.0.4 (testdata/pluscodes.txt.gz:
// latitude and longitude bits, code).
func TestPlusCodeJava(t *testing.T) {
	f, err := os.Open("testdata/pluscodes.txt.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(gz)
	n := 0
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		latBits, _ := strconv.ParseInt(parts[0], 10, 64)
		lonBits, _ := strconv.ParseInt(parts[1], 10, 64)
		lat, lon := math.Float64frombits(uint64(latBits)), math.Float64frombits(uint64(lonBits))
		if got := PlusCode(lat, lon); got != parts[2] {
			t.Errorf("%v,%v: %s, Java %s", lat, lon, got, parts[2])
		}
		n++
	}
	if n < 20000 {
		t.Fatalf("only %d vectors", n)
	}
}
