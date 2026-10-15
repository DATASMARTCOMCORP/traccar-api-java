package config

import (
	"fmt"
	"regexp"
)

// Keys and defaults used by the Go server, with the names and defaults of
// org.traccar.config.Keys. Add keys here as features are ported, with the Java default.
const (
	DatabaseDriver      = "database.driver"
	DatabaseURL         = "database.url"
	DatabaseUser        = "database.user"
	DatabasePassword    = "database.password"
	DatabaseMaxPoolSize = "database.maxPoolSize" // default 20

	// DatabaseTimezone is Go-only. Java stores timestamps as wall-clock time in the JVM's
	// default zone. This names that zone (e.g. "America/Sao_Paulo") when the Go host's
	// zone differs from the JVM's. Default: the host zone (TZ or /etc/localtime), which
	// is also how the JVM picks its default.
	DatabaseTimezone = "database.timezone"

	WebAddress = "web.address"
	WebPort    = "web.port" // default 8082
	WebPath    = "web.path" // default "./web"

	LoggerConsole = "logger.console"
	LoggerFile    = "logger.file"  // default "./logs/tracker-server.log"
	LoggerLevel   = "logger.level" // default "info"

	ProtocolsEnable = "protocols.enable"
)

const (
	DefaultDatabaseMaxPoolSize = 20
	DefaultWebPort             = 8082
	DefaultWebPath             = "./web"
	DefaultLoggerFile          = "./logs/tracker-server.log"
	DefaultLoggerLevel         = "info"
)

// Port returns <protocol>.port with Java's default (see defaultPorts). 0 means the protocol
// is off.
func (c *Config) Port(protocol string) (int, error) {
	return c.Int(protocol+".port", defaultPorts[protocol])
}

var listSeparator = regexp.MustCompile(`[, ]`)

// ProtocolEnabled reports whether protocol is allowed by protocols.enable. Without that
// key, every protocol with a port is enabled. As in Java, the list is split on commas and
// spaces.
func (c *Config) ProtocolEnabled(protocol string) bool {
	v, ok := c.Lookup(ProtocolsEnable)
	if !ok {
		return true
	}
	for _, name := range listSeparator.Split(v, -1) {
		if name == protocol {
			return true
		}
	}
	return false
}

// CheckDriver rejects databases other than Postgres, the only one the Go server supports.
// database.driver may be absent (Java then infers it from the URL).
func (c *Config) CheckDriver() error {
	if d, ok := c.Lookup(DatabaseDriver); ok && d != "org.postgresql.Driver" {
		return fmt.Errorf("config %s: %q is not supported, only org.postgresql.Driver", DatabaseDriver, d)
	}
	return nil
}
