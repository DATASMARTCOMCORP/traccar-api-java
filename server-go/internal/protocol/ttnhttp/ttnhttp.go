// Package ttnhttp is The Things Network protocol (TtnHttpProtocol): uplink webhooks posted
// as JSON over HTTP.
package ttnhttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "ttnhttp",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is TtnHttpProtocolDecoder.
type Decoder struct{}

// number is getJsonNumber(key).doubleValue(): a missing number throws.
func number(o *kit.JSONObject, key string) float64 {
	n := o.Number(key)
	if n == nil {
		kit.Throw("NullPointerException: no number %q", key)
	}
	return n.Double()
}

func object(o *kit.JSONObject, key string) *kit.JSONObject {
	v := o.Object(key)
	if v == nil {
		kit.Throw("NullPointerException: no object %q", key)
	}
	return v
}

func parseDate(s string) model.Time {
	t, err := javatime.ParseISOZoned(s)
	if err != nil {
		kit.Throw("DateTimeParseException: %v", err)
	}
	return model.NewTime(t)
}

func status(code int) *ingest.HTTPResponse { return &ingest.HTTPResponse{Status: code} }

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		return status(http.StatusBadRequest), nil, nil
	}

	root := kit.ParseJSONObject(body)

	ds := m.Session(object(root, "end_device_ids").String("device_id"))
	if ds == nil {
		return status(http.StatusNotFound), nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	network := model.NewNetwork()

	message := root.Object("uplink_message")
	if message == nil {
		return status(http.StatusBadRequest), nil, nil
	}

	payload := message.Object("decoded_payload")
	if payload == nil {
		return status(http.StatusOK), nil, nil
	}

	metadata := message.Array("rx_metadata")
	if metadata == nil {
		kit.Throw("NullPointerException: no rx_metadata")
	}
	if first := kit.JSONObjectOf(metadata[0]); first.Has("time") {
		t := parseDate(first.String("time"))
		pos.DeviceTime, pos.FixTime = t, t
	} else {
		t := parseDate(message.String("received_at"))
		pos.DeviceTime, pos.FixTime = t, t
	}

	hasLocation := false

	for _, key := range payload.Keys() {
		if strings.HasPrefix(key, "gps_") {
			location := object(payload, key)
			pos.SetLatitude(number(location, "latitude"))
			pos.SetLongitude(number(location, "longitude"))
			pos.Altitude = number(location, "altitude")
			hasLocation = true
			continue
		}
		switch key {
		case "latitude":
			pos.SetLatitude(number(payload, key))
			hasLocation = true
		case "longitude":
			pos.SetLongitude(number(payload, key))
			hasLocation = true
		case "altitude":
			pos.Altitude = number(payload, key)
		case "accuracy":
			pos.Accuracy = number(payload, key)
		case "sats":
			pos.Set(model.KeySatellites, kit.JSONNumberOf(payload.Get(key)).Int())
		case "speed":
			pos.Speed = m.ConvertSpeed(number(payload, key), "kn")
		case "heading":
			pos.Course = number(payload, key)
		case "time":
			t := parseDate(payload.String(key))
			pos.DeviceTime, pos.FixTime = t, t
		case "wifi":
			networks := object(payload, key)
			for _, macAddress := range networks.Keys() {
				networkObject := object(networks, macAddress)
				ap := model.NewWifiAccessPoint(macAddress, networkObject.Int("rssi"))
				if networkObject.Has("channel") {
					channel := int64(networkObject.Int("channel"))
					ap.Channel = &channel
				}
				network.AddWifiAccessPoint(ap)
			}
		default:
			decodeJSONValue(pos, key, payload.Get(key))
		}
	}

	if hasLocation {
		pos.Valid = true
	} else {
		m.LastLocation(pos, nil)
	}

	if network.WifiAccessPoints != nil {
		pos.Network = network
	}

	return status(http.StatusOK), []*model.Position{pos}, nil
}

func decodeJSONValue(pos *model.Position, key string, value any) {
	switch v := value.(type) {
	case string:
		pos.Set(key, v)
	case kit.JSONNumber:
		pos.Set(key, v.Double())
	case bool:
		pos.Set(key, v)
	}
}
