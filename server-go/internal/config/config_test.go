package config

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func noEnv(string) (string, bool) { return "", false }

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func mustParse(t *testing.T, xml string, lookupEnv func(string) (string, bool)) *Config {
	t.Helper()
	c, err := Parse(strings.NewReader(xml), lookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const sample = `<?xml version='1.0' encoding='UTF-8'?>
<!DOCTYPE properties SYSTEM 'http://java.sun.com/dtd/properties.dtd'>
<properties>
    <comment>SIR test</comment>
    <!-- XML comments are ignored -->
    <entry key='database.url'>jdbc:postgresql://db.example:5432/traccar?ssl=true&amp;sslmode=require</entry>
    <entry key='database.password' />
    <entry key="web.port">8082</entry>
    <entry key='padded'>  spaced value  </entry>
    <entry key='cdata'><![CDATA[a<b>&c]]></entry>
    <entry key='dup'>first</entry>
    <entry key='dup'>second</entry>
    <entry key='bool.upper'>TRUE</entry>
    <entry key='bool.yes'>yes</entry>
    <entry key='bool.empty'></entry>
    <entry key='int.bad'>12a</entry>
    <entry key='int.space'> 12</entry>
    <entry key='float.space'> 1.5 </entry>
    <entry key='multi'>line1
line2</entry>
</properties>`

func TestParse(t *testing.T) {
	c := mustParse(t, sample, noEnv)
	tests := []struct {
		key, want string
		set       bool
	}{
		{"database.url", "jdbc:postgresql://db.example:5432/traccar?ssl=true&sslmode=require", true},
		{"database.password", "", true}, // present but empty
		{"web.port", "8082", true},
		{"padded", "  spaced value  ", true}, // Java doesn't trim
		{"cdata", "a<b>&c", true},
		{"dup", "second", true},
		{"multi", "line1\nline2", true},
		{"missing", "", false},
	}
	for _, tt := range tests {
		got, ok := c.Lookup(tt.key)
		if got != tt.want || ok != tt.set {
			t.Errorf("Lookup(%q) = %q, %v; want %q, %v", tt.key, got, ok, tt.want, tt.set)
		}
	}
}

func TestTypedAccessors(t *testing.T) {
	c := mustParse(t, sample, noEnv)
	if !c.Bool("bool.upper", false) {
		t.Error("TRUE should parse as true")
	}
	if c.Bool("bool.yes", true) {
		t.Error("only \"true\" is true, like Boolean.parseBoolean")
	}
	if c.Bool("bool.empty", true) {
		t.Error("a present empty value is false, not the default")
	}
	if !c.Bool("bool.missing", true) {
		t.Error("absent key should give the default")
	}
	if n, err := c.Int("web.port", 1); n != 8082 || err != nil {
		t.Errorf("Int(web.port) = %d, %v", n, err)
	}
	if n, err := c.Int("int.missing", 20); n != 20 || err != nil {
		t.Errorf("Int(default) = %d, %v", n, err)
	}
	if _, err := c.Int("int.bad", 1); err == nil {
		t.Error("want error for non-integer")
	}
	if _, err := c.Int("int.space", 1); err == nil {
		t.Error("Integer.parseInt rejects surrounding spaces; so must we")
	}
	if f, err := c.Float("float.space", 0); f != 1.5 || err != nil {
		t.Errorf("Float = %v, %v (Double.parseDouble trims)", f, err)
	}
	if s := c.String("missing", "dflt"); s != "dflt" {
		t.Errorf("String default = %q", s)
	}
	if s := c.String("database.password", "dflt"); s != "" {
		t.Errorf("present empty String = %q, want empty", s)
	}
}

func TestParseErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"not xml":       "database.url=x",
		"wrong root":    "<config><entry key='a'>b</entry></config>",
		"missing key":   "<properties><entry>b</entry></properties>",
		"nested":        "<properties><entry key='a'><b/></entry></properties>",
		"unknown child": "<properties><item key='a'>b</item></properties>",
		"unclosed":      "<properties><entry key='a'>b</properties>",
		"empty":         "",
	} {
		if _, err := Parse(strings.NewReader(doc), noEnv); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestEnvName(t *testing.T) {
	// Vectors from org.traccar.config.ConfigTest.
	for key, want := range map[string]string{
		"database.url":                            "DATABASE_URL",
		"database.checkConnection":                "DATABASE_CHECK_CONNECTION",
		"database.maxPoolSize":                    "DATABASE_MAX_POOL_SIZE",
		"deviceManager.lookupGroupsAttribute":     "DEVICE_MANAGER_LOOKUP_GROUPS_ATTRIBUTE",
		"command.fallbackToSms":                   "COMMAND_FALLBACK_TO_SMS",
		"status.timeout":                          "STATUS_TIMEOUT",
		"suntech_udp.port":                        "SUNTECH_UDP_PORT",
		"server.buffering.threshold":              "SERVER_BUFFERING_THRESHOLD",
		"database.registerUnknown.defaultGroupId": "DATABASE_REGISTER_UNKNOWN_DEFAULT_GROUP_ID",
	} {
		if got := EnvName(key); got != want {
			t.Errorf("EnvName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestEnvironmentOverride(t *testing.T) {
	vars := map[string]string{
		"DATABASE_URL":      "jdbc:postgresql://env/db",
		"DATABASE_PASSWORD": "", // empty: doesn't override, but counts for Has
		"WEB_PATH":          "/srv/web",
	}

	off := mustParse(t, sample, env(vars))
	if v, _ := off.Lookup("database.url"); !strings.Contains(v, "db.example") {
		t.Errorf("without the switch, env must be ignored; got %q", v)
	}
	if off.Has("web.path") {
		t.Error("without the switch, env must not count for Has")
	}

	vars["CONFIG_USE_ENVIRONMENT_VARIABLES"] = "True"
	on := mustParse(t, sample, env(vars))
	if v, _ := on.Lookup("database.url"); v != "jdbc:postgresql://env/db" {
		t.Errorf("env override: got %q", v)
	}
	if v, ok := on.Lookup("web.path"); v != "/srv/web" || !ok {
		t.Errorf("env-only key: got %q, %v", v, ok)
	}
	if v, ok := on.Lookup("database.password"); v != "" || !ok {
		t.Errorf("empty env must fall back to the file: got %q, %v", v, ok)
	}
	if !on.Has("database.password") || !on.Has("web.path") {
		t.Error("Has must count env variables, even empty ones")
	}

	// The switch can also come from the file itself.
	delete(vars, "CONFIG_USE_ENVIRONMENT_VARIABLES")
	fromFile := mustParse(t, strings.Replace(sample, "<comment>",
		"<entry key='config.useEnvironmentVariables'>true</entry><comment>", 1), env(vars))
	if v, _ := fromFile.Lookup("database.url"); v != "jdbc:postgresql://env/db" {
		t.Errorf("config.useEnvironmentVariables in file: got %q", v)
	}
}

func TestPortsAndProtocols(t *testing.T) {
	c := mustParse(t, `<properties>
		<entry key='suntech.port'>47762</entry>
		<entry key='protocols.enable'>osmand,suntech  atrack</entry>
	</properties>`, noEnv)
	for proto, want := range map[string]int{"suntech": 47762, "osmand": 5055, "intellitrac": 5037, "atrack": 5044, "unknown": 0} {
		if got, err := c.Port(proto); got != want || err != nil {
			t.Errorf("Port(%s) = %d, %v; want %d", proto, got, err, want)
		}
	}
	for proto, want := range map[string]bool{"osmand": true, "suntech": true, "atrack": true, "intellitrac": false, "": true} {
		if got := c.ProtocolEnabled(proto); got != want {
			t.Errorf("ProtocolEnabled(%q) = %v, want %v", proto, got, want)
		}
	}
	all := mustParse(t, `<properties/>`, noEnv)
	if !all.ProtocolEnabled("intellitrac") {
		t.Error("without protocols.enable every protocol is enabled")
	}
}

func TestCheckDriver(t *testing.T) {
	for driver, ok := range map[string]bool{"org.postgresql.Driver": true, "org.h2.Driver": false, "com.mysql.cj.jdbc.Driver": false} {
		c := mustParse(t, "<properties><entry key='database.driver'>"+driver+"</entry></properties>", noEnv)
		if err := c.CheckDriver(); (err == nil) != ok {
			t.Errorf("CheckDriver(%s) = %v", driver, err)
		}
	}
	if err := mustParse(t, "<properties/>", noEnv).CheckDriver(); err != nil {
		t.Errorf("absent driver: %v", err)
	}
}

// TestDefaultPortsMatchJava re-reads PortConfigSuffix.java so the table can't drift.
func TestDefaultPortsMatchJava(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "src", "main", "java", "org", "traccar", "config", "PortConfigSuffix.java"))
	if err != nil {
		t.Skip("Java sources not found:", err)
	}
	re := regexp.MustCompile(`put\(([A-Za-z0-9]+)Protocol\.class, (\d+)\)`)
	java := map[string]int{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		port, _ := strconv.Atoi(m[2])
		java[strings.ToLower(m[1])] = port
	}
	if len(java) < 250 || !maps.Equal(java, defaultPorts) {
		t.Errorf("defaultPorts (%d entries) differs from PortConfigSuffix.java (%d entries)", len(defaultPorts), len(java))
	}
	for name, port := range map[string]int{"osmand": 5055, "suntech": 5011, "gps103": 5001, "teltonika": 5027} {
		if defaultPorts[name] != port {
			t.Errorf("%s: %d, want %d", name, defaultPorts[name], port)
		}
	}
}

// TestRepositoryFiles parses the real config files in this repository: the format must
// load exactly as Java loads it. Only non-secret values are asserted.
func TestRepositoryFiles(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	c, err := Load(filepath.Join(root, "setup", "traccar.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Lookup("database.driver"); v != "org.h2.Driver" {
		t.Errorf("setup/traccar.xml database.driver = %q", v)
	}
	if v, ok := c.Lookup("database.password"); v != "" || !ok {
		t.Errorf("setup/traccar.xml database.password = %q, %v", v, ok)
	}

	path := filepath.Join(root, "sir-dev.xml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("sir-dev.xml not present")
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := c.Port("suntech"); p != 47762 || err != nil {
		t.Errorf("sir-dev.xml suntech port = %d, %v", p, err)
	}
	if p, err := c.Port("intellitrac"); p != 6037 || err != nil {
		t.Errorf("sir-dev.xml intellitrac port = %d, %v", p, err)
	}
	if !c.Bool("atrack.custom", false) {
		t.Error("sir-dev.xml atrack.custom should be true")
	}
	if err := c.CheckDriver(); err != nil {
		t.Error(err)
	}
}
