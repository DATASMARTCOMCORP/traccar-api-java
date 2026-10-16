// Package ramac is the Ramac protocol (RamacProtocol): JSON posted over HTTP.
package ramac

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "ramac", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

// Decoder is RamacProtocolDecoder.
type Decoder struct{}

func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	json := kit.ParseJSONObject([]byte(kit.UTF8(body)))
	ds := m.Session(json.String("DeviceId"))
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}
	date := kit.NewDateFormat("yyyy-MM-dd HH:mm:ss", m.Zone())

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyType, json.Int("PacketType"))
	pos.Set(model.KeyIndex, json.Int("SeqNumber"))
	deviceTime := date.Parse(json.String("UpdateDate"))
	pos.DeviceTime = model.NewTime(deviceTime)

	if alert := json.Int("Alert"); alert > 0 {
		pos.Set("alert", alert)
		if message := json.String("AlertMessage"); message != "" {
			pos.Set("alertMessage", message)
		}
	}
	if json.Has("GpsEvent") {
		pos.Set("gpsEvent", json.Int("GpsEvent"))
		if json.Has("GpsEventText") {
			pos.Set("gpsEventText", json.String("GpsEventText"))
		}
	}
	if json.Has("Event") {
		pos.Set(model.KeyEvent, json.Int("Event"))
	}
	if json.Has("BatteryPercentage") {
		pos.Set(model.KeyBatteryLevel, json.Int("BatteryPercentage"))
	}
	if json.Has("Battery") {
		pos.Set(model.KeyBattery, number(json, "Battery"))
	}
	pos.Set("deviceType", json.String("DeviceTypeText"))

	if json.Has("Latitude") && json.Has("Longitude") {
		pos.Valid = true
		if json.Has("LocationDateTime") {
			pos.FixTime = model.NewTime(date.Parse(json.String("LocationDateTime")))
		} else {
			pos.FixTime = pos.DeviceTime
		}
		pos.SetLatitude(number(json, "Latitude"))
		pos.SetLongitude(number(json, "Longitude"))
	} else {
		m.LastLocation(pos, &deviceTime)
	}
	return &ingest.HTTPResponse{Status: http.StatusOK, Body: []byte(`{"CaseID":1,"EventID":1}`)}, []*model.Position{pos}, nil
}

// number is getJsonNumber(key).doubleValue(): a null value throws.
func number(json *kit.JSONObject, key string) float64 {
	n := json.Number(key)
	if n == nil {
		kit.Throw("NullPointerException: %s", key)
	}
	return n.Double()
}
