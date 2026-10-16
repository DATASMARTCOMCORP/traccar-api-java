package kit

import (
	"bufio"
	"compress/gzip"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// testdata/format-vectors.tsv.gz holds String.format results of Java 21 for doubles
// (raw bits), ints, longs, bytes, shorts and strings.
func TestFormatJavaVectors(t *testing.T) {
	f, err := os.Open("testdata/format-vectors.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(zr)
	n, failures := 0, 0
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 4)
		format, kind, value, want := parts[0], parts[1], parts[2], parts[3]
		var arg any
		switch kind {
		case "D":
			bits, _ := strconv.ParseUint(value, 16, 64)
			arg = math.Float64frombits(bits)
		case "I":
			v, _ := strconv.ParseInt(value, 10, 32)
			arg = int(v)
		case "J":
			v, _ := strconv.ParseInt(value, 10, 64)
			arg = v
		case "B":
			v, _ := strconv.ParseInt(value, 10, 8)
			arg = int8(v)
		case "S":
			v, _ := strconv.ParseInt(value, 10, 16)
			arg = int16(v)
		case "T":
			arg = value
		}
		n++
		if got := Format(format, arg); got != want {
			failures++
			if failures <= 20 {
				t.Errorf("Format(%q, %s %v) = %q, Java %q", format, kind, arg, got, want)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d differ", failures, n)
	}
	if n < 20000 {
		t.Fatalf("only %d vectors", n)
	}
}
