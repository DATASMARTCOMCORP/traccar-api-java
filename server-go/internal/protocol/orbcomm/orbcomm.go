// Package orbcomm is the ORBCOMM protocol (OrbcommProtocol): the server polls ORBCOMM's
// gateway over HTTP for the messages of its devices.
package orbcomm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

const maxHTTPLength = 64 * 1024 // BaseProtocol.MAX_HTTP_LENGTH

// Protocol is the protocol's definition: an HTTP client connection to orbcomm.address and
// the protocol's port. As in Java, orbcomm.devices must be set.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	if _, ok := cfg.Lookup("orbcomm.devices"); !ok {
		return ingest.Protocol{}, fmt.Errorf("orbcomm.devices is not set (TrackerClient fails without it)")
	}
	interval, err := cfg.Int64("orbcomm.interval", 0)
	if err != nil {
		return ingest.Protocol{}, err
	}
	accessID, hasAccessID := cfg.Lookup("orbcomm.accessId")
	password, hasPassword := cfg.Lookup("orbcomm.password")
	host, _ := cfg.Lookup("orbcomm.address")
	return ingest.Protocol{
		Name: "orbcomm",
		Clients: []ingest.Client{{NewPipeline: func() ingest.Pipeline {
			poller := &Poller{start: time.Now()}
			return ingest.Pipeline{
				Framer:  ingest.HTTPResponseFramer(maxHTTPLength),
				Decoder: &Decoder{Poller: poller},
				Poll: &ingest.Poller{Interval: time.Duration(interval) * time.Second, Request: func(m *ingest.Message) {
					m.Send(poller.request(accessID, hasAccessID, password, hasPassword, host))
				}},
			}
		}}},
	}, nil
}

var dateFormat = kit.NewDateFormat("yyyy-MM-dd HH:mm:ss", time.UTC)

// Poller is OrbcommProtocolPoller: it asks for the messages since the start time the last
// response gave.
type Poller struct {
	mu    sync.Mutex
	start time.Time
}

func (p *Poller) setStart(t time.Time) {
	p.mu.Lock()
	p.start = t
	p.mu.Unlock()
}

// request is the HTTP request HttpRequestEncoder writes.
func (p *Poller) request(accessID string, hasAccessID bool, password string, hasPassword bool, host string) []byte {
	p.mu.Lock()
	start := p.start
	p.mu.Unlock()
	uri := "/GLGW/2/RestMessages.svc/JSON/get_return_messages/" +
		"?" + param("access_id", accessID, hasAccessID) +
		"&" + param("password", password, hasPassword) +
		"&" + param("start_utc", start.UTC().Format("2006-01-02 15:04:05"), true)
	return []byte("GET " + uri + " HTTP/1.1\r\nhost: " + host + "\r\ncontent-length: 0\r\n\r\n")
}

// param is QueryStringEncoder.addParam: UTF-8 percent-encoding of all but a-z, A-Z, 0-9,
// '-', '_', '.' and '*'; a null value is the bare name.
func param(name, value string, ok bool) string {
	if !ok {
		return encode(name)
	}
	return encode(name) + "=" + encode(value)
}

func encode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '*':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Decoder is OrbcommProtocolDecoder.
type Decoder struct {
	Poller *Poller
}

func member[T any](o map[string]any, key string) T {
	v, ok := o[key].(T)
	if !ok {
		kit.Throw("JSON member %q: %T", key, o[key])
	}
	return v
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	response := msg.(*ingest.HTTPResponse)
	dec := json.NewDecoder(bytes.NewReader([]byte(kit.UTF8(response.Body))))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		panic(fmt.Errorf("JsonParsingException: %w", err))
	}
	o, ok := root.(map[string]any)
	if !ok {
		kit.Throw("JsonException: not an object")
	}

	if m.HasChannel() {
		if next := member[string](o, "NextStartUTC"); next != "" && d.Poller != nil {
			d.Poller.setStart(dateFormat.Parse(next))
		}
	}

	raw, present := o["Messages"]
	if !present {
		kit.Throw("NullPointerException: no Messages")
	}
	if raw == nil {
		return nil
	}
	messages, ok := raw.([]any)
	if !ok {
		kit.Throw("ClassCastException: Messages")
	}
	positions := []*model.Position{}
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			kit.Throw("ClassCastException: message")
		}
		ds := m.Session(member[string](message, "MobileID"))
		if ds == nil {
			continue
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.DeviceTime = model.NewTime(dateFormat.Parse(member[string](message, "MessageUTC")))

		fields := member[[]any](member[map[string]any](message, "Payload"), "Fields")
		for _, item := range fields {
			field, ok := item.(map[string]any)
			if !ok {
				kit.Throw("ClassCastException: field")
			}
			value := member[string](field, "Value")
			switch strings.ToLower(member[string](field, "Name")) {
			case "eventtime":
				pos.DeviceTime = model.NewTime(time.UnixMilli(kit.Long(value) * 1000))
			case "latitude":
				pos.SetLatitude(float64(kit.Int(value)) / 60000.0)
			case "longitude":
				pos.SetLongitude(float64(kit.Int(value)) / 60000.0)
			case "speed":
				pos.Speed = units.KnotsFromKph(float64(kit.Int(value)))
			case "heading":
				heading := kit.Int(value)
				if heading > 360 {
					heading = 0
				}
				pos.Course = float64(heading)
			}
		}
		if pos.Latitude != 0 && pos.Longitude != 0 {
			pos.Valid = true
			pos.FixTime = pos.DeviceTime
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}
		positions = append(positions, pos)
	}
	if len(positions) == 0 {
		return nil
	}
	return positions
}
