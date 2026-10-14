// Package config reads the server configuration: the same XML properties file and keys
// as the Java server, with the same CONFIG_USE_ENVIRONMENT_VARIABLES override.
//
// The file is Java's Properties XML format:
//
//	<!DOCTYPE properties SYSTEM 'http://java.sun.com/dtd/properties.dtd'>
//	<properties>
//	    <entry key='database.url'>jdbc:postgresql://localhost/traccar</entry>
//	</properties>
//
// When environment variables are enabled, a non-empty variable named after the key
// (DATABASE_URL for database.url, DATABASE_MAX_POOL_SIZE for database.maxPoolSize)
// overrides the file.
package config

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Config holds the properties from the file. It is read-only after Load.
type Config struct {
	props     map[string]string
	useEnv    bool
	lookupEnv func(string) (string, bool)
}

// Load reads the configuration file at path, with overrides from the process environment.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c, err := Parse(f, os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse reads a configuration from r. lookupEnv is os.LookupEnv outside of tests.
func Parse(r io.Reader, lookupEnv func(string) (string, bool)) (*Config, error) {
	props, err := parseProperties(r)
	if err != nil {
		return nil, fmt.Errorf("configuration file is not a valid XML document: %w", err)
	}
	c := &Config{props: props, lookupEnv: lookupEnv}
	env, _ := lookupEnv("CONFIG_USE_ENVIRONMENT_VARIABLES")
	c.useEnv = parseBool(env) || parseBool(props["config.useEnvironmentVariables"])
	return c, nil
}

// Lookup returns the value of key and whether it is set. An environment override wins
// when it is non-empty; a key present in the file with an empty value is set to "".
func (c *Config) Lookup(key string) (string, bool) {
	if c.useEnv {
		if v, ok := c.lookupEnv(EnvName(key)); ok && v != "" {
			return v, true
		}
	}
	v, ok := c.props[key]
	return v, ok
}

// Has reports whether key is set. Like Java's Config.hasKey, an environment variable
// counts even when empty.
func (c *Config) Has(key string) bool {
	if c.useEnv {
		if _, ok := c.lookupEnv(EnvName(key)); ok {
			return true
		}
	}
	_, ok := c.props[key]
	return ok
}

// String returns the value of key, or def when unset.
func (c *Config) String(key, def string) string {
	if v, ok := c.Lookup(key); ok {
		return v
	}
	return def
}

// Bool returns the value of key, or def when unset. Like Java's Boolean.parseBoolean,
// only "true" (any case) is true; any other set value, including "", is false.
func (c *Config) Bool(key string, def bool) bool {
	if v, ok := c.Lookup(key); ok {
		return parseBool(v)
	}
	return def
}

// Int returns the value of key, or def when unset. A set value that isn't an integer
// is an error, where Java would throw NumberFormatException.
func (c *Config) Int(key string, def int) (int, error) {
	v, ok := c.Lookup(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("config %s: %q is not an integer", key, v)
	}
	return int(n), nil
}

// Int64 is Int for keys Java reads as long.
func (c *Config) Int64(key string, def int64) (int64, error) {
	v, ok := c.Lookup(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config %s: %q is not an integer", key, v)
	}
	return n, nil
}

// Float returns the value of key, or def when unset. Like Java's Double.parseDouble,
// surrounding whitespace is ignored.
func (c *Config) Float(key string, def float64) (float64, error) {
	v, ok := c.Lookup(key)
	if !ok {
		return def, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, fmt.Errorf("config %s: %q is not a number", key, v)
	}
	return f, nil
}

// EnvName returns the environment variable that overrides key: dots become
// underscores, an underscore goes before each upper-case letter, and the result is
// upper-cased. "database.maxPoolSize" becomes "DATABASE_MAX_POOL_SIZE".
func EnvName(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r == '.':
			b.WriteByte('_')
		case unicode.IsUpper(r):
			b.WriteByte('_')
			b.WriteRune(r)
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

func parseBool(s string) bool { return strings.EqualFold(s, "true") }

// parseProperties reads Java's Properties XML format: a <properties> root holding an
// optional <comment> and <entry key="..."> elements. Values are kept exactly as
// written (Java doesn't trim them). A repeated key keeps its last value.
func parseProperties(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	props := map[string]string{}
	depth := 0
	var key string
	var value strings.Builder
	inEntry := false
	sawRoot := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 1:
				if t.Name.Local != "properties" {
					return nil, fmt.Errorf("root element is <%s>, want <properties>", t.Name.Local)
				}
				sawRoot = true
			case depth == 2 && t.Name.Local == "entry":
				var ok bool
				for _, a := range t.Attr {
					if a.Name.Local == "key" {
						key, ok = a.Value, true
					}
				}
				if !ok {
					return nil, errors.New("<entry> without key attribute")
				}
				inEntry = true
				value.Reset()
			case depth == 2 && t.Name.Local == "comment":
			default:
				return nil, fmt.Errorf("unexpected element <%s>", t.Name.Local)
			}
		case xml.EndElement:
			if depth == 2 && inEntry {
				props[key] = value.String()
				inEntry = false
			}
			depth--
		case xml.CharData:
			if inEntry {
				value.Write(t)
			}
		}
	}
	if !sawRoot {
		return nil, errors.New("no <properties> element")
	}
	return props, nil
}
