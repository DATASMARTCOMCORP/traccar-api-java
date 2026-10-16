// Package oracle checks Go protocol decoders, frame decoders and command encoders against
// the outputs of their Java originals. testdata/java-vectors.jsonl.gz records every
// decoder call of Traccar's protocol tests (src/test/java/org/traccar/protocol), with the
// full decoded positions, made by running those tests with a recording ProtocolTest; the
// SIR Suntech vectors come from the pre-merge SuntechProtocolDecoder (MIGRATION.md §8).
//
// The Java tests only check ranges ("latitude in [-90, 90]"); these checks are exact:
// every field, every attribute with its JSON text and number type, and errors where Java
// threw.
package oracle

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

//go:embed testdata/java-vectors.jsonl.gz
var vectorsGz []byte

// Record is one recorded call.
type Record struct {
	Kind     string   `json:"kind"` // decode, frame, frameNull, command
	Decoder  string   `json:"decoder"`
	Instance int64    `json:"instance"`
	Input    *Input   `json:"input"`
	Output   *Output  `json:"output"`
	Error    string   `json:"error"`
	Test     string   `json:"test"`
	Method   string   `json:"method"`
	Line     int      `json:"line"`
	Now      int64    `json:"now"`
	Before   int64    `json:"before"`
	Created  []string `json:"created"`
	Expected string   `json:"expected"`
	Encoder  string   `json:"encoder"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
}

// Input is a decoder's input.
type Input struct {
	Type    string     `json:"type"` // bytes, string, http, ...
	Hex     string     `json:"hex"`
	Text    string     `json:"text"`
	Method  string     `json:"method"`
	URI     string     `json:"uri"`
	Headers [][]string `json:"headers"`
	Status  int        `json:"status"`
}

// Output is a decoder's result.
type Output struct {
	Position  *Position   `json:"position"`
	Positions []*Position `json:"positions"`
	Bytes     *string     `json:"bytes"`
	Text      *string     `json:"text"`
	Other     string      `json:"other"`
}

// Position is a recorded position: doubles as raw bits, times as epoch milliseconds.
type Position struct {
	DeviceID    int64             `json:"deviceId"`
	Protocol    *string           `json:"protocol"`
	DeviceTime  *int64            `json:"deviceTime"`
	FixTime     *int64            `json:"fixTime"`
	Outdated    bool              `json:"outdated"`
	Valid       bool              `json:"valid"`
	Latitude    int64             `json:"latitude"`
	Longitude   int64             `json:"longitude"`
	Altitude    int64             `json:"altitude"`
	Speed       int64             `json:"speed"`
	Course      int64             `json:"course"`
	Accuracy    int64             `json:"accuracy"`
	Address     *string           `json:"address"`
	Attributes  string            `json:"attributes"`
	Network     *string           `json:"network"`
	Types       map[string]string `json:"types"`
	GeofenceIDs []int64           `json:"geofenceIds"`
}

var (
	loadOnce sync.Once
	records  []Record
	loadErr  error
)

// Records returns the recorded calls of a Java test class (e.g. "SuntechProtocolDecoderTest").
func Records(test string) ([]Record, error) {
	loadOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(vectorsGz))
		if err != nil {
			loadErr = err
			return
		}
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 16<<20), 16<<20)
		for sc.Scan() {
			var r Record
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				loadErr = err
				return
			}
			records = append(records, r)
		}
		loadErr = sc.Err()
	})
	var out []Record
	for _, r := range records {
		if r.Test == test {
			out = append(out, r)
		}
	}
	return out, loadErr
}

// Env is the decoder environment of Traccar's BaseTest: device 1 is known once a decoder
// has passed unique ids, the config is empty, and replies go nowhere.
type Env struct {
	Cfg         *config.Config
	identified  bool
	ProtocolTag string
	Sent        []any
	// UniqueID is the device's unique id: encoder tests mock "123456789012345", decoder
	// tests leave it null.
	UniqueID string
}

// NewEnv returns a fresh environment (one per decoder instance, as inject() makes).
func NewEnv() *Env {
	cfg, _ := config.Parse(strings.NewReader(`<properties></properties>`), func(string) (string, bool) { return "", false })
	return &Env{Cfg: cfg, ProtocolTag: "unknown"}
}

func (e *Env) Protocol() string       { return e.ProtocolTag }
func (e *Env) Config() *config.Config { return e.Cfg }

// DeviceSession is BaseTest's mock: null until a call with unique ids, then always a new
// session of device 1.
func (e *Env) DeviceSession(_ context.Context, _ string, ids []string) (*ingest.DeviceSession, error) {
	if !e.identified {
		if len(ids) == 0 {
			return nil, nil
		}
		e.identified = true
	}
	return &ingest.DeviceSession{DeviceID: 1}, nil
}

func (e *Env) QueuedCommands(context.Context, int64, int) ([]model.QueuedCommand, error) {
	return nil, nil
}

func (e *Env) UpdateNotificationToken(context.Context, int64, string) error { return nil }

func (e *Env) Device(_ context.Context, id int64) (*model.Device, error) {
	return &model.Device{ID: 1, UniqueID: e.UniqueID}, nil
}

func (e *Env) Lookup(context.Context, int64, string, bool, bool) (any, error) { return nil, nil }

func (e *Env) Send(_ string, data any) { e.Sent = append(e.Sent, data) }

// Zone is the zone the Java tests ran in.
func (e *Env) Zone() *time.Location { return javatime.TimeZone("America/Sao_Paulo") }

// WriteMedia gives null, as the mocked MediaManager.
func (e *Env) WriteMedia(string, string, []byte) string { return "" }

// LocalAddress is "": tests pass a null channel.
func (e *Env) LocalAddress() string { return "" }

// Message returns a message for a recorded call: its clock is the recording's (from
// before the call when recorded).
func (e *Env) Message(r Record) *ingest.Message {
	return ingest.NewMessage(context.Background(), e, "", time.UnixMilli(clock(r)))
}

func clock(r Record) int64 {
	if r.Before != 0 {
		return r.Before
	}
	return r.Now
}

// DecodeInput turns a recorded input into what the Go decoder receives: a *kit.Buf for
// bytes, a string for strings, an *HTTPRequest for HTTP requests.
func DecodeInput(in *Input) (any, error) {
	switch in.Type {
	case "bytes":
		b, err := hex.DecodeString(in.Hex)
		if err != nil {
			return nil, err
		}
		return kit.NewBuf(b), nil
	case "string":
		return in.Text, nil
	case "mqtt": // the message's bytes, as MqttEncoder writes it
		b, err := hex.DecodeString(in.Hex)
		if err != nil {
			return nil, err
		}
		return kit.NewBuf(b), nil
	case "http":
		b, err := hex.DecodeString(in.Hex)
		if err != nil {
			return nil, err
		}
		return &HTTPRequest{Method: in.Method, URI: in.URI, Headers: in.Headers, Body: b}, nil
	}
	return nil, fmt.Errorf("unsupported input type %s", in.Type)
}

// HTTPRequest is a recorded FullHttpRequest.
type HTTPRequest struct {
	Method  string
	URI     string
	Headers [][]string
	Body    []byte
}

// Header returns a header value (case-insensitive name), as Netty's headers.get.
func (r *HTTPRequest) Header(name string) string {
	for _, h := range r.Headers {
		if strings.EqualFold(h[0], name) {
			return h[1]
		}
	}
	return ""
}

// Handlers makes a protocol's Go handlers for the replay of a Java test class.
type Handlers struct {
	// Decoder makes the decoder for one Java decoder instance of a test method.
	Decoder func(method string, env *Env) ingest.Decoder
	// Before, if set, runs before each decoder call: tests that change a decoder's
	// settings between calls (decoder.setHbm(2)) do it here, by the record's line.
	Before func(r Record, d ingest.Decoder)
	// Encoder makes the command encoder; decoder is the decoder the Java test passed
	// along (nil if none).
	Encoder func(method string, decoder ingest.Decoder) ingest.CommandEncoder
	// Framer makes the frame decoder of a test method (one per method, as Java's).
	Framer func(method string) ingest.Framer
}

// Run replays the recorded calls of a Java test class, in order, through Go handlers:
// decoder calls with one decoder (and environment) per Java decoder instance, frame
// decoder checks, and command encodings.
func Run(t *testing.T, test string, h Handlers) {
	t.Helper()
	recs, err := Records(test)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatalf("no recorded calls for %s", test)
	}
	type key struct {
		method   string
		instance int64
	}
	decoders := map[key]ingest.Decoder{}
	envs := map[key]*Env{}
	framers := map[string]ingest.Framer{}
	decoder := func(r Record) (ingest.Decoder, *Env) {
		k := key{r.Method, r.Instance}
		if decoders[k] == nil {
			if h.Decoder == nil {
				t.Fatalf("%s: no Go decoder", test)
			}
			envs[k] = NewEnv()
			decoders[k] = h.Decoder(r.Method, envs[k])
		}
		return decoders[k], envs[k]
	}
	counts := map[string]int{}
	defer func() { t.Logf("%s: replayed %v", test, counts) }()
	for _, r := range recs {
		where := fmt.Sprintf("%s:%d", r.Method, r.Line)
		counts[r.Kind]++
		switch r.Kind {
		case "decode":
			d, env := decoder(r)
			if h.Before != nil {
				h.Before(r, d)
			}
			checkDecode(t, where, r, d, env)
		case "frame", "frameNull":
			if len(r.Created) == 0 {
				continue // the check of an encoder's output, recorded as "command"
			}
			if h.Framer == nil {
				if h.Encoder != nil {
					continue // an encoder test checking part of its output with verifyFrame
				}
				t.Fatalf("%s: no Go frame decoder", test)
			}
			if framers[r.Method] == nil {
				framers[r.Method] = h.Framer(r.Method)
			}
			checkFrame(t, where, r, framers[r.Method])
		case "command", "encode":
			if h.Encoder == nil {
				t.Fatalf("%s: no Go encoder", test)
			}
			var d ingest.Decoder
			if r.Instance != 0 {
				d, _ = decoder(r)
			}
			checkEncode(t, where, r, h.Encoder(r.Method, d))
		}
	}
}

func checkDecode(t *testing.T, where string, r Record, d ingest.Decoder, env *Env) {
	t.Helper()
	msg, err := DecodeInput(r.Input)
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	got, gotErr := decode(d, env.Message(r), msg)
	if r.Error != "" {
		if gotErr == nil {
			t.Errorf("%s: Java threw %q, Go returned %s (input %s)", where, r.Error, describe(got), inputText(r.Input))
		}
		return
	}
	if gotErr != nil {
		t.Errorf("%s: Go failed: %v (input %s)", where, gotErr, inputText(r.Input))
		return
	}
	var want []*Position
	if r.Output != nil {
		if r.Output.Position != nil {
			want = []*Position{r.Output.Position}
		} else {
			want = r.Output.Positions
			if want == nil {
				want = []*Position{}
			}
		}
	}
	if (got == nil) != (want == nil) {
		t.Errorf("%s: Go returned %s, Java %s (input %s)", where, nullOrList(got), nullOrList(want), inputText(r.Input))
		return
	}
	if len(got) != len(want) {
		t.Errorf("%s: %d positions, Java %d (input %s)", where, len(got), len(want), inputText(r.Input))
		return
	}
	for i := range want {
		for _, diff := range comparePosition(got[i], want[i], r) {
			t.Errorf("%s position %d: %s (input %s)", where, i, diff, inputText(r.Input))
		}
	}
}

func checkFrame(t *testing.T, where string, r Record, f ingest.Framer) {
	t.Helper()
	in, err := hex.DecodeString(r.Created[len(r.Created)-1])
	if err != nil {
		t.Fatal(err)
	}
	env := NewEnv()
	var got any
	var gotErr error
	func() {
		defer kit.Recover(&gotErr)
		got = f.Frame(env.Message(r), kit.NewBuf(in))
	}()
	want := "<null>"
	if r.Output != nil && r.Output.Bytes != nil {
		want = *r.Output.Bytes
	}
	if text := outputText(got, gotErr); text != want {
		t.Errorf("%s: frame\n\tgot  %s\n\tJava %s", where, text, want)
	}
}

func checkEncode(t *testing.T, where string, r Record, e ingest.CommandEncoder) {
	t.Helper()
	c, err := parseCommand(r.Command)
	if err != nil {
		t.Fatalf("%s: command %s: %v", where, r.Command, err)
	}
	env := NewEnv()
	env.UniqueID = "123456789012345"
	m := env.Message(r)
	if len(r.Args) > 0 {
		// The time argument of encodeCommand(command, time).
		var ms int64
		fmt.Sscan(r.Args[0], &ms)
		m.Now = time.UnixMilli(ms)
	}
	var got any
	var gotErr error
	func() {
		defer kit.Recover(&gotErr)
		got = e.Encode(m, &c)
	}()
	want := "<null>"
	if r.Output != nil {
		switch {
		case r.Output.Bytes != nil:
			want = *r.Output.Bytes
		case r.Output.Text != nil:
			want = "text " + *r.Output.Text
		}
	}
	if r.Kind == "command" {
		want = r.Expected
	}
	if text := outputText(got, gotErr); text != want {
		t.Errorf("%s: command %s\n\tgot  %q\n\tJava %q", where, r.Command, text, want)
	}
}

// outputText renders a frame or encoder output as the records do: hex for bytes, "text "
// and the string for strings.
func outputText(v any, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	switch x := v.(type) {
	case nil:
		return "<null>"
	case *kit.Buf:
		if x == nil {
			return "<null>"
		}
		return hex.EncodeToString(x.Bytes())
	case []byte:
		return hex.EncodeToString(x)
	case string:
		return "text " + x
	}
	return fmt.Sprintf("%T", v)
}

func inputText(in *Input) string {
	if in.Type == "string" {
		return fmt.Sprintf("%q", in.Text)
	}
	if in.Type == "http" {
		return in.Method + " " + in.URI
	}
	if len(in.Hex) > 120 {
		return in.Hex[:120] + "..."
	}
	return in.Hex
}

func nullOrList[T any](v []T) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%d positions", len(v))
}

func describe(p []*model.Position) string { return fmt.Sprintf("%d positions", len(p)) }

func decode(d ingest.Decoder, m *ingest.Message, msg any) (result []*model.Position, err error) {
	defer kit.Recover(&err)
	switch v := d.Decode(m, msg).(type) {
	case nil:
	case *model.Position:
		if v != nil {
			result = []*model.Position{v}
		}
	case []*model.Position:
		result = v
	default:
		return nil, fmt.Errorf("decoder returned %T", v)
	}
	return result, nil
}

// comparePosition compares a Go position with Java's. A time Java took from its clock
// (between the record's before and now) matches the Go message's clock.
func comparePosition(g *model.Position, w *Position, r Record) []string {
	var d []string
	eq := func(name string, got, want any) {
		if fmt.Sprint(got) != fmt.Sprint(want) {
			d = append(d, fmt.Sprintf("%s: got %v, Java %v", name, got, want))
		}
	}
	bits := func(name string, got float64, want int64) {
		if int64(math.Float64bits(got)) != want && !(math.IsNaN(got) && math.IsNaN(math.Float64frombits(uint64(want)))) {
			d = append(d, fmt.Sprintf("%s: got %v, Java %v", name, got, math.Float64frombits(uint64(want))))
		}
	}
	ms := func(name string, got model.Time, want *int64) {
		switch {
		case want == nil && !got.IsZero():
			d = append(d, fmt.Sprintf("%s: got %v, Java null", name, got.Time.UTC()))
		case want != nil && got.IsZero():
			d = append(d, fmt.Sprintf("%s: got null, Java %v", name, time.UnixMilli(*want).UTC()))
		case want != nil && got.Time.UnixMilli() == clock(r) && r.Before != 0 && r.Before <= *want && *want <= r.Now:
		case want != nil && got.Time.UnixMilli() != *want:
			d = append(d, fmt.Sprintf("%s: got %v, Java %v", name, got.Time.UTC(), time.UnixMilli(*want).UTC()))
		}
	}
	eq("deviceId", g.DeviceID, w.DeviceID)
	gotProtocol := ""
	if g.Protocol != nil {
		gotProtocol = *g.Protocol
	}
	wantProtocol := ""
	if w.Protocol != nil {
		wantProtocol = *w.Protocol
	}
	eq("protocol", gotProtocol, wantProtocol)
	ms("deviceTime", g.DeviceTime, w.DeviceTime)
	ms("fixTime", g.FixTime, w.FixTime)
	eq("outdated", g.Outdated, w.Outdated)
	eq("valid", g.Valid, w.Valid)
	bits("latitude", g.Latitude, w.Latitude)
	bits("longitude", g.Longitude, w.Longitude)
	bits("altitude", g.Altitude, w.Altitude)
	bits("speed", g.Speed, w.Speed)
	bits("course", g.Course, w.Course)
	bits("accuracy", g.Accuracy, w.Accuracy)
	gotAddress, wantAddress := "<null>", "<null>"
	if g.Address != nil {
		gotAddress = *g.Address
	}
	if w.Address != nil {
		wantAddress = *w.Address
	}
	eq("address", gotAddress, wantAddress)
	attrs, err := g.Attributes.MarshalJSON()
	if err != nil {
		d = append(d, "attributes: "+err.Error())
	} else if string(attrs) != w.Attributes {
		d = append(d, fmt.Sprintf("attributes:\n\tgot  %s\n\tJava %s", attrs, w.Attributes))
	}
	for k, javaType := range w.Types {
		v, _ := g.Attributes.Get(k)
		if family(v) != javaFamily(javaType) {
			d = append(d, fmt.Sprintf("attribute %s: Go %T, Java %s", k, v, javaType))
		}
	}
	gotNetwork := "<null>"
	if g.Network != nil {
		b, _ := g.Network.MarshalJSON()
		gotNetwork = string(b)
	}
	wantNetwork := "<null>"
	if w.Network != nil {
		wantNetwork = *w.Network
	}
	if gotNetwork != wantNetwork {
		d = append(d, fmt.Sprintf("network:\n\tgot  %s\n\tJava %s", gotNetwork, wantNetwork))
	}
	return d
}

func family(v any) string {
	switch v.(type) {
	case int64:
		return "integer"
	case float64, model.Float:
		return "double"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

func javaFamily(t string) string {
	switch t {
	case "Integer", "Long", "Short", "Byte", "BigInteger":
		return "integer"
	case "Double", "Float":
		return "double"
	case "String":
		return "string"
	case "Boolean":
		return "boolean"
	case "null":
		return "null"
	}
	return t
}

func parseCommand(text string) (model.Command, error) {
	var j struct {
		ID          int64            `json:"id"`
		Attributes  model.Attributes `json:"attributes"`
		DeviceID    int64            `json:"deviceId"`
		Type        string           `json:"type"`
		TextChannel bool             `json:"textChannel"`
		Description *string          `json:"description"`
	}
	if err := json.Unmarshal([]byte(text), &j); err != nil {
		return model.Command{}, err
	}
	return model.Command{ID: j.ID, Attributes: j.Attributes, DeviceID: j.DeviceID, Type: j.Type,
		TextChannel: j.TextChannel, Description: j.Description}, nil
}

// Tests lists the Java test classes with recorded calls, for coverage reports.
func Tests() ([]string, error) {
	if _, err := Records(""); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, r := range records {
		seen[r.Test] = true
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// HTTPDecoder adapts an HTTP protocol's decoder to the replay: recorded requests become
// *http.Request values, and a decoder error is a Java exception.
func HTTPDecoder(d ingest.HTTPDecoder) ingest.Decoder {
	return ingest.DecoderFunc(func(m *ingest.Message, msg any) any {
		in := msg.(*HTTPRequest)
		r, err := http.NewRequest(in.Method, in.URI, bytes.NewReader(in.Body))
		if err != nil {
			panic(err)
		}
		r.RequestURI = in.URI
		for _, h := range in.Headers {
			r.Header.Add(h[0], h[1])
		}
		_, positions, err := d.DecodeHTTP(m.Context(), m, r, in.Body)
		if err != nil {
			panic(err)
		}
		if positions == nil {
			return nil
		}
		return positions
	})
}
