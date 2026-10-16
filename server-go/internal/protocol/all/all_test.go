package all

import (
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
)

// TestEveryPackageRegistered keeps the registry in step with the protocol packages, and
// builds every protocol with an empty configuration.
func TestEveryPackageRegistered(t *testing.T) {
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	names := Names()
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "kit" || e.Name() == "oracle" || e.Name() == "all" {
			continue
		}
		if !slices.Contains(names, e.Name()) {
			t.Errorf("package %s is not registered", e.Name())
		}
	}
	cfg, err := config.Parse(stringsReader(`<properties><entry key="orbcomm.devices">1</entry></properties>`), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		newProtocol, _ := Lookup(name)
		p, err := newProtocol(cfg, time.UTC)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.Name != name {
			t.Errorf("%s: protocol named %q", name, p.Name)
		}
		if len(p.Servers)+len(p.Clients) == 0 {
			t.Errorf("%s: no servers", name)
		}
	}
}

func stringsReader(s string) *stringReader { return &stringReader{s: s} }

type stringReader struct{ s string }

func (r *stringReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	n := copy(p, r.s)
	r.s = r.s[n:]
	return n, nil
}
