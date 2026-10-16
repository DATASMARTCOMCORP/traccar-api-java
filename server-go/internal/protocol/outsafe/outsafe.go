// Package outsafe is the Outsafe protocol (OutsafeProtocol): JSON positions posted over
// HTTP.
package outsafe

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "outsafe", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

// Decoder is OutsafeProtocolDecoder.
type Decoder struct{}

// number is getJsonNumber(key) unboxed: a missing or null value throws.
func number(o *kit.JSONObject, key string) kit.JSONNumber {
	n := o.Number(key)
	if n == nil {
		kit.Throw("NullPointerException: no number %q", key)
	}
	return *n
}

// DecodeHTTP decodes one request; Java's exceptions are panics.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	json := kit.ParseJSONObject([]byte(kit.UTF8(body)))

	ds := m.Session(json.String("device"))
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(m.Now)
	pos.Valid = true
	pos.SetLatitude(number(json, "latitude").Double())
	pos.SetLongitude(number(json, "longitude").Double())
	pos.Altitude = number(json, "altitude").Double()
	pos.Course = float64(number(json, "heading").Int())

	pos.Set(model.KeyRssi, number(json, "rssi").Int())
	pos.Set("origin", json.String("origin"))

	data := json.Object("data")
	if data == nil {
		kit.Throw("NullPointerException: no data")
	}
	for _, key := range data.Keys() {
		switch v := data.Get(key).(type) {
		case kit.JSONNumber:
			pos.Set(key, v.Double())
		case string:
			pos.Set(key, v)
		case bool:
			pos.Set(key, v)
		}
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, []*model.Position{pos}, nil
}
