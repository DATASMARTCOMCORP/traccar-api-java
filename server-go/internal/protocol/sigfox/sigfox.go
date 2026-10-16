// Package sigfox is the Sigfox protocol (SigfoxProtocol): JSON callbacks posted over HTTP,
// with the location in fields or in a hex payload.
package sigfox

import (
	"context"
	"net/http"
	"regexp"
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
		Name:    "sigfox",
		Servers: []ingest.Server{{HTTP: &Decoder{}}},
	}
}

// Decoder is SigfoxProtocolDecoder.
type Decoder struct {
	// ModelOverride is setModelOverride: the device model to assume ("" for none).
	ModelOverride string
}

func (d *Decoder) deviceModel(m *ingest.Message, ds *ingest.DeviceSession) string {
	if d.ModelOverride != "" {
		return d.ModelOverride
	}
	return m.DeviceModel(ds)
}

// jsonContains: the key is present (JSON null included) and isn't the string "null".
func jsonContains(json *kit.JSONObject, key string) bool {
	if !json.Has(key) {
		return false
	}
	if s, ok := json.Get(key).(string); ok {
		return s != "null"
	}
	return true
}

func getJSONBoolean(json *kit.JSONObject, key string) bool {
	switch v := json.Get(key).(type) {
	case string:
		return strings.EqualFold(v, "true")
	case bool:
		return v
	}
	return false
}

func getJSONInt(json *kit.JSONObject, key string) int {
	switch v := json.Get(key).(type) {
	case kit.JSONNumber:
		return v.Int()
	case string:
		return kit.Int(v)
	}
	return 0
}

func getJSONDouble(json *kit.JSONObject, key string) float64 {
	switch v := json.Get(key).(type) {
	case kit.JSONNumber:
		return v.Double()
	case string:
		return kit.Double(v)
	}
	return 0
}

var macPairs = regexp.MustCompile(`(..)`)

// DecodeHTTP decodes one request; a panic is where the Java decoder throws.
func (d *Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	content := kit.UTF8(body)
	if !strings.HasPrefix(content, "{") {
		part := kit.Split(content, "=")[0]
		decoded, err := kit.DecodeComponent(part, 0, len(part))
		if err != nil {
			return nil, nil, err
		}
		content = decoded
	}
	json := kit.ParseJSONObject([]byte(content))

	var deviceID string
	if json.Has("device") {
		deviceID = json.String("device")
	} else {
		deviceID = json.String("deviceId")
	}

	ds := m.Session(deviceID)
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	switch {
	case jsonContains(json, "time"):
		pos.SetTime(time.UnixMilli(int64(getJSONInt(json, "time")) * 1000))
	case jsonContains(json, "positionTime"):
		pos.SetTime(time.UnixMilli(int64(getJSONInt(json, "positionTime")) * 1000))
	default:
		pos.SetTime(m.Now)
	}

	if jsonContains(json, "lastSeen") {
		pos.DeviceTime = model.NewTime(time.UnixMilli(int64(getJSONInt(json, "lastSeen")) * 1000))
	}

	if jsonContains(json, "location") ||
		jsonContains(json, "lat") && jsonContains(json, "lng") && !jsonContains(json, "data") ||
		jsonContains(json, "latitude") && jsonContains(json, "longitude") && !jsonContains(json, "data") {

		location := json
		if jsonContains(json, "location") {
			location = json.Object("location")
		}
		pos.Valid = true
		latKey, lngKey := "latitude", "longitude"
		if jsonContains(location, "lat") {
			latKey = "lat"
		}
		pos.SetLatitude(getJSONDouble(location, latKey))
		if jsonContains(location, "lng") {
			lngKey = "lng"
		}
		pos.SetLongitude(getJSONDouble(location, lngKey))

	} else if jsonContains(json, "data") {

		buf := kit.NewBuf(kit.ParseHex(json.String("data")))
		header := buf.ReadUnsignedByte()
		if d.deviceModel(m, ds) == "Amber" {
			flags := int64(buf.ReadUnsignedByte())
			pos.Set(model.KeyMotion, kit.Check(flags, 1))
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/50.0)
			pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))
			pos.Valid = true
			pos.SetLatitude(float64(buf.ReadInt()) / 60000.0)
			pos.SetLongitude(float64(buf.ReadInt()) / 60000.0)
		} else if header == 0x0f || header == 0x1f {
			pos.Valid = header>>4 > 0
			pos.SetLatitude(float64(kit.ReadSignedMagnitudeInt(buf)) / 1000000.0)
			pos.SetLongitude(float64(kit.ReadSignedMagnitudeInt(buf)) / 1000000.0)
			pos.Set(model.KeyBattery, buf.ReadUnsignedByte())
		} else if header>>4 <= 3 && buf.WriterIndex() == 12 {
			if kit.ToInt(header, 4) == 0 {
				pos.Valid = true
				pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.Course = float64(buf.ReadUnsignedByte() * 2)
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/40.0)
			} else {
				return nil, nil, nil // Java returns null without a response
			}
		}

	} else if jsonContains(json, "payload") {

		buf := kit.NewBuf(kit.ParseHex(json.String("payload")))
		event := buf.ReadUnsignedByte()
		pos.Set(model.KeyEvent, event)
		if event == 0x22 || event == 0x62 {
			pos.AddAlarm(model.AlarmSos)
		}
		for buf.IsReadable() {
			switch buf.ReadUnsignedByte() {
			case 0x01:
				pos.Valid = true
				pos.SetLatitude(float64(buf.ReadMedium()))
				pos.SetLongitude(float64(buf.ReadMedium()))
			case 0x02:
				pos.Valid = true
				pos.SetLatitude(float64(buf.ReadFloat()))
				pos.SetLongitude(float64(buf.ReadFloat()))
			case 0x03:
				pos.Set(model.PrefixTemp+"1", float64(buf.ReadSignedByte())*0.5)
			case 0x04:
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/10.0)
			case 0x05:
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			case 0x06:
				mac := macPairs.ReplaceAllString(buf.ReadSlice(6).HexDump(), "$1:")
				pos.Network = model.NetworkOfWifi(model.NewWifiAccessPoint(
					mac[:len(mac)-1], buf.ReadUnsignedByte()))
			case 0x07:
				buf.Skip(10) // wifi extended
			case 0x08:
				buf.Skip(6) // accelerometer
			case 0x09:
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
			default:
				buf.ReadUnsignedByte() // fence number
			}
		}

	}

	if pos.Latitude == 0 && pos.Longitude == 0 {
		t := pos.DeviceTime.Time
		m.LastLocation(pos, &t)
	}

	if jsonContains(json, "moving") {
		pos.Set(model.KeyMotion, getJSONBoolean(json, "moving"))
	}
	if jsonContains(json, "magStatus") {
		pos.Set(model.KeyBlocked, getJSONBoolean(json, "magStatus"))
	}
	if jsonContains(json, "temperature") {
		pos.Set(model.KeyDeviceTemp, getJSONDouble(json, "temperature"))
	}
	if jsonContains(json, "rssi") {
		pos.Set(model.KeyRssi, getJSONDouble(json, "rssi"))
	}
	if jsonContains(json, "seqNumber") {
		pos.Set(model.KeyIndex, getJSONInt(json, "seqNumber"))
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, []*model.Position{pos}, nil
}
