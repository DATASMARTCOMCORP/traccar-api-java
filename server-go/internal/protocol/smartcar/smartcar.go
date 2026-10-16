// Package smartcar is the Smartcar protocol (SmartcarProtocol): Smartcar webhook JSON
// posted over HTTP, with its VERIFY challenge.
package smartcar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "smartcar",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is SmartcarProtocolDecoder.
type Decoder struct{}

var ok = &ingest.HTTPResponse{Status: http.StatusOK}

// DecodeHTTP decodes one request; a panic is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	root := kit.ParseJSONObject([]byte(kit.UTF8(body)))
	eventType := root.String("eventType")
	data := root.Object("data")

	if eventType == "VERIFY" {
		challenge := data.String("challenge")
		managementToken, found := m.Config().Lookup("smartcar.managementToken")
		if !found {
			kit.Throw("NullPointerException: smartcar.managementToken")
		}
		mac := hmac.New(sha256.New, []byte(managementToken))
		mac.Write([]byte(challenge))
		signed := hex.EncodeToString(mac.Sum(nil))
		response, err := json.Marshal(map[string]string{"challenge": signed})
		if err != nil {
			return nil, nil, err
		}
		return &ingest.HTTPResponse{Status: http.StatusOK, Body: response}, nil, nil
	}

	if eventType != "VEHICLE_STATE" {
		return ok, nil, nil
	}

	ds := m.Session(root.String("vehicleId"))
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(time.UnixMilli(root.Object("meta").Number("deliveredAt").Long()))

	signals := data.Array("signals")
	if signals == nil {
		kit.Throw("NullPointerException: signals")
	}
	for _, signalValue := range signals {
		signal := kit.JSONObjectOf(signalValue)
		body := signal.Object("body")
		code := signal.String("code")
		value := body.Get("value")
		unit, _ := body.Get("unit").(string)

		number, isNumber := value.(kit.JSONNumber)
		flag, isBool := value.(bool)

		switch code {
		case "location-preciselocation":
			pos.Valid = true
			pos.SetLatitude(body.Number("latitude").Double())
			pos.SetLongitude(body.Number("longitude").Double())
			meta := signal.Object("meta")
			pos.FixTime = model.NewTime(time.UnixMilli(meta.Number("oemUpdatedAt").Long()))
			pos.DeviceTime = model.NewTime(time.UnixMilli(meta.Number("fetchedAt").Long()))
		case "tractionbattery-stateofcharge":
			if isNumber {
				pos.Set(model.KeyBatteryLevel, number.Double())
			}
		case "charge-ischarging":
			if isBool {
				pos.Set(model.KeyCharge, flag)
			}
		case "charge-voltage":
			if isNumber {
				pos.Set(model.KeyPower, number.Double())
			}
		case "odometer":
			if isNumber {
				if strings.EqualFold(unit, "miles") {
					pos.Set(model.KeyOdometer, number.Double()*1609.344)
				} else {
					pos.Set(model.KeyOdometer, number.Double()*1000.0)
				}
			}
		case "speed":
			if isNumber {
				if strings.EqualFold(unit, "miles_per_hour") {
					pos.Speed = units.KnotsFromMph(number.Double())
				} else {
					pos.Speed = units.KnotsFromKph(number.Double())
				}
			}
		default:
			if isNumber {
				pos.Set(code, number.Double())
			} else if isBool {
				pos.Set(code, flag)
			}
		}
	}

	if !pos.Valid {
		t := pos.DeviceTime.Time
		m.LastLocation(pos, &t)
	}

	return ok, []*model.Position{pos}, nil
}
