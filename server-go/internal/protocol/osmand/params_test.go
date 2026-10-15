package osmand

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// testdata/query-vectors.tsv: random query strings through Netty 4.2.14's
// QueryStringDecoder, with a path (P, the request URI) or without (B, a form body):
// hex input, then hex of name\x01value\x02... in Java's iteration order, or ERR where
// Netty throws. QUERY_VECTORS names a larger file of the same format.
func TestNettyVectors(t *testing.T) {
	check(t, "testdata/query-vectors.tsv")
	if path := os.Getenv("QUERY_VECTORS"); path != "" {
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
	n, failures := 0, 0
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		input, err := hex.DecodeString(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		params, err := queryParams(string(input), fields[0] == "P")
		got := "ERR"
		if err == nil {
			var b strings.Builder
			for _, p := range params {
				for _, v := range p.values {
					b.WriteString(p.name + "\x01" + v + "\x02")
				}
			}
			got = hex.EncodeToString([]byte(b.String()))
		}
		n++
		if got != fields[2] {
			if failures++; failures <= 20 {
				want, _ := hex.DecodeString(fields[2])
				gotText, _ := hex.DecodeString(got)
				t.Errorf("%s %q: got %q, Netty %q", fields[0], input, gotText, want)
			}
		}
	}
	t.Logf("%s: %d inputs, %d mismatches", path, n, failures)
}
