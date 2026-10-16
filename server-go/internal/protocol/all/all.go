// Package all lists the ported device protocols, for the server to start them.
package all

import (
	"sort"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/atrack"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/intellitrac"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/osmand"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/suntech"
)

// A Constructor makes a protocol's definition from the configuration; zone is the
// server's time zone (Java's default zone).
type Constructor func(cfg *config.Config, zone *time.Location) (ingest.Protocol, error)

func fixed(p func() ingest.Protocol) Constructor {
	return func(*config.Config, *time.Location) (ingest.Protocol, error) { return p(), nil }
}

func withConfig(p func(*config.Config) (ingest.Protocol, error)) Constructor {
	return func(cfg *config.Config, _ *time.Location) (ingest.Protocol, error) { return p(cfg) }
}

// protocols are the ported protocols by name (Java's BaseProtocol.getName).
var protocols = map[string]Constructor{
	"atrack":      withConfig(atrack.Protocol),
	"intellitrac": fixed(intellitrac.Protocol),
	"osmand":      osmand.Protocol,
	"suntech":     fixed(suntech.Protocol),
}

// Names returns the ported protocols' names, sorted.
func Names() []string {
	var names []string
	for name := range protocols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns a protocol's constructor.
func Lookup(name string) (Constructor, bool) {
	c, ok := protocols[name]
	return c, ok
}
