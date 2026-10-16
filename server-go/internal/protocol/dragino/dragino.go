// Package dragino is the Dragino protocol (DraginoProtocol): The Things Network uplink
// JSON posted over HTTP.
package dragino

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "dragino",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is DraginoProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes one uplink: 400 for an unknown device, 200 with its position
// otherwise. Errors are where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	root := parseObject(kit.UTF8(body))

	ds := m.Session(root.object("end_device_ids").str("device_id"))
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}

	message := root.object("uplink_message")
	decoded := message.object("decoded_payload")

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t, err := time.Parse(time.RFC3339Nano, message.str("received_at")) // ISO_ZONED_DATE_TIME
	if err != nil {
		kit.Throw("DateTimeParseException: %v", err)
	}
	pos.SetTime(t)

	pos.Valid = true
	pos.SetLatitude(decoded.double("Latitude"))
	pos.SetLongitude(decoded.double("Longitude"))

	pos.Set(model.KeyHumidity, decoded.double("Hum"))
	pos.Set(model.KeyBattery, decoded.double("BatV"))
	pos.Set(model.PrefixTemp+"1", decoded.double("Tem"))

	if strings.EqualFold(decoded.str("ALARM_status"), "true") { // Boolean.parseBoolean
		pos.AddAlarm(model.AlarmSos)
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, []*model.Position{pos}, nil
}

// object is a JSON object read as javax.json's JsonObject: the getters throw when a key
// is missing or has another type.
type object map[string]any

func parseObject(s string) object {
	d := json.NewDecoder(bytes.NewReader([]byte(s)))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil || v == nil {
		kit.Throw("JsonParsingException: %v", err)
	}
	return v
}

func (o object) get(key string) any {
	v, ok := o[key]
	if !ok {
		kit.Throw("NullPointerException: %s", key)
	}
	return v
}

func (o object) str(key string) string {
	v, ok := o.get(key).(string)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return v
}

// double is getJsonNumber(key).doubleValue().
func (o object) double(key string) float64 {
	n, ok := o.get(key).(json.Number)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	v, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		kit.Throw("NumberFormatException: %v", err)
	}
	return v
}

func (o object) object(key string) object {
	v, ok := o.get(key).(map[string]any)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return v
}
