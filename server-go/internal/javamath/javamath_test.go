package javamath

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// testdata/vectors.bin holds records of x, Math.sin(x), Math.cos(x), y, z, Math.atan2(y, z)
// as big-endian double bits, written by Java 21.0.12 on x86_64 (special values, then
// random inputs from several distributions). JAVAMATH_VECTORS names a larger file of the
// same format to check as well.
func TestJavaVectors(t *testing.T) {
	check(t, "testdata/vectors.bin")
	if path := os.Getenv("JAVAMATH_VECTORS"); path != "" {
		check(t, path)
	}
}

func check(t *testing.T, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = 6 * 8
	if len(data) == 0 || len(data)%size != 0 {
		t.Fatalf("%s: bad size %d", path, len(data))
	}
	same := func(a, b float64) bool {
		return math.Float64bits(a) == math.Float64bits(b) || math.IsNaN(a) && math.IsNaN(b)
	}
	failures := 0
	fail := func(format string, args ...any) {
		if failures++; failures <= 10 {
			t.Errorf(format, args...)
		}
	}
	for i := 0; i < len(data); i += size {
		var v [6]float64
		for j := range v {
			v[j] = math.Float64frombits(binary.BigEndian.Uint64(data[i+8*j:]))
		}
		if got := Sin(v[0]); !same(got, v[1]) {
			fail("Sin(%v) = %v, Java %v", v[0], got, v[1])
		}
		if got := Cos(v[0]); !same(got, v[2]) {
			fail("Cos(%v) = %v, Java %v", v[0], got, v[2])
		}
		if got := Atan2(v[3], v[4]); !same(got, v[5]) {
			fail("Atan2(%v, %v) = %v, Java %v", v[3], v[4], got, v[5])
		}
	}
	t.Logf("%s: %d records, %d mismatches", path, len(data)/size, failures)
}

func BenchmarkSin(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Sin(float64(i) * 1e-3)
	}
}
