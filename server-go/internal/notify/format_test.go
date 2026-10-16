package notify

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

type testCase struct {
	Name         string
	User         *model.User
	Server       *model.Server
	Device       *model.Device
	Event        *model.Event
	Position     *model.Position
	Geofence     *model.Geofence
	Maintenance  *model.Maintenance
	Driver       *model.Driver
	Translations map[string]string
}

type render struct {
	Case, Set, Lang, Template string
	Subject, Digest           *string
	Body                      string
	Error                     string
}

// TestJavaRenders replays testdata/renders.jsonl.gz (recorder/record.sh): every
// notification template of every language, and production's customized ones, rendered by
// Java's Velocity for each case in testdata/cases.json.
func TestJavaRenders(t *testing.T) {
	data, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []testCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	byName := map[string]testCase{}
	for _, c := range cases {
		byName[c.Name] = c
	}
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	f := &Formatter{WebURL: "http://sir.example:8082", Zone: zone, Token: func(int64) (string, error) { return "TOKEN123", nil }}

	file, err := os.Open("testdata/renders.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	n, failed := 0, 0
	for sc.Scan() {
		var r render
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		c := byName[r.Case]
		in := Input{User: c.User, Server: c.Server, Device: c.Device, Event: c.Event, Position: c.Position}
		if c.Event.GeofenceID != 0 {
			in.Geofence = c.Geofence
		}
		if c.Event.MaintenanceID != 0 {
			in.Maintenance = c.Maintenance
		}
		if id := c.Event.Attributes.JavaString("driverUniqueId"); id != nil {
			in.DriverSet = true
			if c.Driver != nil && c.Driver.UniqueID == *id {
				in.Driver = c.Driver
			}
		}
		ctx := f.context(in)
		ctx["translations"] = map[string]string{}
		for k, v := range c.Translations {
			ctx["translations"].(map[string]string)[k] = v
		}
		dir := filepath.Join("..", "..", "..", "templates", "notifications")
		if r.Set == "prod" {
			dir = filepath.Join("testdata", "prod")
		}
		m, err := renderFile(filepath.Join(dir, r.Lang, r.Template+".vm"), ctx, false)
		n++
		label := r.Case + " " + r.Set + "/" + r.Lang + "/" + r.Template
		switch {
		case r.Error != "":
			if err == nil {
				failed++
				t.Errorf("%s: Java failed (%s), Go rendered", label, r.Error)
			}
			continue
		case err != nil:
			failed++
			t.Errorf("%s: %v", label, err)
			continue
		}
		digest := r.Body
		if r.Digest != nil {
			digest = *r.Digest
		}
		if (m.Subject == nil) != (r.Subject == nil) || m.Subject != nil && *m.Subject != *r.Subject {
			failed++
			t.Errorf("%s: subject\n got %v\nJava %v", label, deref(m.Subject), deref(r.Subject))
		}
		if m.Digest != digest {
			failed++
			t.Errorf("%s: digest\n got %q\nJava %q", label, m.Digest, digest)
		}
		if m.Body != r.Body {
			failed++
			t.Errorf("%s: body\n got %q\nJava %q", label, m.Body, r.Body)
		}
		if failed > 20 {
			t.Fatal("too many differences")
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d renders compared", n)
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
