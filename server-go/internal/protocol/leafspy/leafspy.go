// Package leafspy is the LeafSpy protocol (LeafSpyProtocol): the Leaf Spy app's HTTP
// requests, with the values as query parameters (or a form body).
package leafspy

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "leafspy",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is LeafSpyProtocolDecoder.
type Decoder struct{}

var badRequest = &ingest.HTTPResponse{Status: http.StatusBadRequest}

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	params, err := queryParams(kit.Latin1([]byte(r.RequestURI)), true)
	if err != nil {
		return nil, nil, err
	}
	if len(params) == 0 {
		if params, err = queryParams(kit.Latin1(body), false); err != nil {
			return nil, nil, err
		}
	}

	pos := m.NewPosition()
	pos.Valid = true
	for _, p := range params {
		for _, value := range p.values {
			switch p.name {
			case "pass":
				ds := m.Session(value)
				if ds == nil {
					return badRequest, nil, nil
				}
				pos.DeviceID = ds.DeviceID
			case "Lat":
				pos.SetLatitude(kit.Double(value))
			case "Long":
				pos.SetLongitude(kit.Double(value))
			case "RPM":
				pos.Set(model.KeyRpm, kit.Int(value))
				pos.Speed = m.ConvertSpeed(kit.Double(value)/63, "kmh")
			case "Elv":
				pos.Altitude = kit.Double(value)
			case "SOC":
				pos.Set(model.KeyBatteryLevel, kit.Double(value))
			case "user":
				pos.Set(model.KeyDriverUniqueID, value)
			case "ChrgMode":
				pos.Set(model.KeyCharge, kit.Int(value) != 0)
			case "Odo":
				pos.Set(model.KeyObdOdometer, int(int32(kit.Int(value))*1000))
			default:
				if v, err := model.ParseDouble(value); err == nil {
					pos.Set(p.name, v)
				} else {
					switch value {
					case "true":
						pos.Set(p.name, true)
					case "false":
						pos.Set(p.name, false)
					default:
						pos.Set(p.name, value)
					}
				}
			}
		}
	}

	if pos.FixTime.IsZero() {
		pos.SetTime(m.Now)
	}
	if pos.Latitude == 0 && pos.Longitude == 0 {
		deviceTime := pos.DeviceTime.Time
		m.LastLocation(pos, &deviceTime)
	}
	if pos.DeviceID != 0 {
		return &ingest.HTTPResponse{Status: http.StatusOK, Body: []byte(`"status":"0"`)}, []*model.Position{pos}, nil
	}
	return badRequest, nil, nil
}
