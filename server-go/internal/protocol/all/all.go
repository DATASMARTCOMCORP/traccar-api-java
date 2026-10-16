// Package all lists the ported device protocols, for the server to start them.
package all

import (
	"sort"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/osmand"
)

// A Constructor makes a protocol's definition from the configuration; zone is the
// server's time zone (Java's default zone).
type Constructor func(cfg *config.Config, zone *time.Location) (ingest.Protocol, error)

// protocols are the ported protocols by name (Java's BaseProtocol.getName).
var protocols = map[string]Constructor{
	"osmand": osmand.Protocol,
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
