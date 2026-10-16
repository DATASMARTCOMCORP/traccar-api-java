// Package polte is the Polte protocol (PolteProtocol): JSON location reports posted over
// HTTP.
package polte

import (
	"context"
	"net/http"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "polte", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

// Decoder is PolteProtocolDecoder.
type Decoder struct{}

// number is getJsonNumber(key) unboxed: a missing or null value throws.
func number(o *kit.JSONObject, key string) kit.JSONNumber {
	n := o.Number(key)
	if n == nil {
		kit.Throw("NullPointerException: no number %q", key)
	}
	return *n
}

// object is getJsonObject(key) dereferenced: a missing or null value throws.
func object(o *kit.JSONObject, key string) *kit.JSONObject {
	v := o.Object(key)
	if v == nil {
		kit.Throw("NullPointerException: no object %q", key)
	}
	return v
}

// DecodeHTTP decodes one request; Java's exceptions are panics. As in Java, a request
// with a location gets no response.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	json := kit.ParseJSONObject([]byte(kit.UTF8(body)))

	ds := m.Session(json.String("ueToken"))
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}

	if !json.Has("location") {
		return &ingest.HTTPResponse{Status: http.StatusOK}, nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	location := object(json, "location")

	pos.Valid = true
	pos.SetTime(time.UnixMilli(int64(location.Int("detected_at")) * 1000))
	pos.SetLatitude(number(location, "latitude").Double())
	pos.SetLongitude(number(location, "longitude").Double())
	pos.Altitude = number(location, "altitude").Double()

	if json.Has("report") {
		report := object(json, "report")
		pos.Set(model.KeyEvent, report.Int("event"))
		if report.Has("battery") {
			battery := object(report, "battery")
			pos.Set(model.KeyBatteryLevel, battery.Int("level"))
			pos.Set(model.KeyBattery, number(battery, "voltage").Double())
		}
	}

	return nil, []*model.Position{pos}, nil
}
