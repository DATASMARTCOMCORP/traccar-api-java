// Package store is the Postgres access layer: the connection pool, explicit SQL per
// entity, the time-zone handling of timestamp columns, and the schema version check.
//
// The database is shared with the Java server, so every write must store exactly the
// text Java stores:
//   - timestamps are "timestamp without time zone" holding wall-clock time in the
//     JVM's zone (see wallClock)
//   - *Id columns hold NULL for 0 and read NULL back as 0 (see id)
//   - attributes, network and geofenceids are Jackson JSON text (see internal/model)
//
// Go never changes the schema. Open checks that the database is at a known Liquibase
// version and refuses to start otherwise (see CheckSchema).
package store

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
)

// Store is the database handle. It is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
	loc  *time.Location // zone of the wall-clock timestamps
}

// Options are the database settings from the config file.
type Options struct {
	URL         string // JDBC URL, as in Java's database.url
	User        string
	Password    string
	MaxPoolSize int
	Location    *time.Location
}

// OptionsFromConfig reads database.* keys with Java's defaults.
func OptionsFromConfig(c *config.Config) (Options, error) {
	if err := c.CheckDriver(); err != nil {
		return Options{}, err
	}
	o := Options{
		URL:      c.String(config.DatabaseURL, ""),
		User:     c.String(config.DatabaseUser, ""),
		Password: c.String(config.DatabasePassword, ""),
		Location: time.Local,
	}
	if o.URL == "" {
		return Options{}, fmt.Errorf("config %s is required", config.DatabaseURL)
	}
	var err error
	if o.MaxPoolSize, err = c.Int(config.DatabaseMaxPoolSize, config.DefaultDatabaseMaxPoolSize); err != nil {
		return Options{}, err
	}
	if name := c.String(config.DatabaseTimezone, ""); name != "" {
		if o.Location, err = time.LoadLocation(name); err != nil {
			return Options{}, fmt.Errorf("config %s: %w", config.DatabaseTimezone, err)
		}
	}
	return o, nil
}

// Open connects, checks the schema version, and returns a ready Store.
func Open(ctx context.Context, o Options) (*Store, error) {
	dsn, ignored, err := DSNFromJDBC(o.URL)
	if err != nil {
		return nil, err
	}
	for _, p := range ignored {
		slog.Warn("ignoring JDBC URL parameter with no Go equivalent", "param", p)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	cfg.ConnConfig.User = o.User
	cfg.ConnConfig.Password = o.Password
	if o.MaxPoolSize > 0 {
		cfg.MaxConns = int32(o.MaxPoolSize)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	s := &Store{pool: pool, loc: o.Location}
	if err := s.CheckSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close waits for queries in progress and closes all connections.
func (s *Store) Close() { s.pool.Close() }

// Location is the zone timestamps are stored in.
func (s *Store) Location() *time.Location { return s.loc }

// DSNFromJDBC converts a JDBC URL (jdbc:postgresql://host:port/db?params) into a pgx
// connection string. Parameters with a pgx equivalent are translated. Others are
// returned so the caller can report them; they don't stop the server, since the JDBC
// driver ignores unknown parameters too.
func DSNFromJDBC(jdbc string) (dsn string, ignored []string, err error) {
	rest, ok := strings.CutPrefix(jdbc, "jdbc:postgresql:")
	if !ok {
		return "", nil, fmt.Errorf("database url %q: only jdbc:postgresql: URLs are supported", jdbc)
	}
	if !strings.HasPrefix(rest, "//") { // jdbc:postgresql:dbname means localhost
		rest = "//localhost/" + rest
	}
	u, err := url.Parse("postgres:" + rest)
	if err != nil {
		return "", nil, fmt.Errorf("database url %q: %w", jdbc, err)
	}
	q := url.Values{}
	sslmode := ""
	ssl := false
	for key, values := range u.Query() {
		v := values[len(values)-1]
		switch key {
		case "sslmode":
			sslmode = v
		case "ssl":
			ssl = v == "" || strings.EqualFold(v, "true")
		case "ApplicationName", "applicationName":
			q.Set("application_name", v)
		case "connectTimeout":
			q.Set("connect_timeout", v)
		case "currentSchema":
			q.Set("search_path", v)
		case "options":
			q.Set("options", v)
		default:
			ignored = append(ignored, key)
		}
	}
	// pgjdbc: ssl=true without sslmode means verify-full; no ssl settings means prefer.
	switch {
	case sslmode != "":
		q.Set("sslmode", sslmode)
	case ssl:
		q.Set("sslmode", "verify-full")
	default:
		q.Set("sslmode", "prefer")
	}
	// pgx unescapes the query like a path, so a space must be %20, not Encode's '+'.
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String(), ignored, nil
}

// id returns the value Java writes for an *Id column: NULL for 0.
func id(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// nullID scans an *Id column, reading NULL as 0.
type nullID struct{ v *int64 }

func (n nullID) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*n.v = 0
	case int64:
		*n.v = x
	case int32:
		*n.v = int64(x)
	default:
		return fmt.Errorf("id column: unexpected %T", src)
	}
	return nil
}

func scanID(v *int64) nullID { return nullID{v} }

// itoa is for building SQL placeholders.
func itoa(i int) string { return strconv.Itoa(i) }
