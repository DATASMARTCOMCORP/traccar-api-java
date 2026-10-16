package model

import (
	"bufio"
	"compress/gzip"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// testdata/float-vectors.tsv.gz holds Float.toString of Java 21 (FloatVectors.java).
func TestJavaFloatVectors(t *testing.T) {
	f, err := os.Open("testdata/float-vectors.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(zr)
	n := 0
	for sc.Scan() {
		bitsText, want, _ := strings.Cut(sc.Text(), "\t")
		bits, _ := strconv.ParseUint(bitsText, 16, 32)
		v := math.Float32frombits(uint32(bits))
		if got := JavaFloat(v); got != want {
			t.Errorf("JavaFloat(%v) = %s, Java %s", v, got, want)
		}
		n++
	}
	if n < 40000 {
		t.Fatalf("only %d vectors", n)
	}
}
