// Package flespi is the Flespi protocol (FlespiProtocol): JSON arrays of flespi messages
// posted over HTTP.
package flespi

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "flespi", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

// Decoder is FlespiProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	result, ok := parseJSON(kit.UTF8(body)).([]any)
	if !ok {
		kit.Throw("JsonException: not an array")
	}
	positions = []*model.Position{}
	for _, v := range result {
		message, ok := v.(*jsonObject)
		if !ok {
			kit.Throw("ClassCastException: not a JsonObject")
		}
		ident, ok := message.values["ident"]
		if !ok {
			continue
		}
		ds := m.Session(str(ident))
		if ds == nil {
			continue
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.Valid = true
		decodePosition(m, message, pos)
		positions = append(positions, pos)
	}
	return &ingest.HTTPResponse{Status: http.StatusOK}, positions, nil
}

func decodePosition(m *ingest.Message, object *jsonObject, pos *model.Position) {
	for _, key := range object.keys {
		value := object.values[key]
		name, index := key, -1
		if kit.IndexOf(key, "#") >= 0 {
			parts := kit.Split(key, "#")
			name = parts[0]
			index = kit.Int(parts[1])
		}
		if !decodeParam(name, index, value, pos) {
			decodeUnknownParam(key, value, pos)
		}
	}
	if pos.Latitude == 0 && pos.Longitude == 0 {
		if pos.DeviceTime.IsZero() {
			m.LastLocation(pos, nil)
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}
	}
}

var alarms = map[string]string{
	"alarm.event.trigger":              model.AlarmGeneral,
	"towing.event.trigger":             model.AlarmTow,
	"towing.alarm.status":              model.AlarmTow,
	"geofence.event.enter":             model.AlarmGeofenceEnter,
	"geofence.event.exit":              model.AlarmGeofenceExit,
	"shock.event.trigger":              model.AlarmVibration,
	"overspeeding.event.trigger":       model.AlarmOverspeed,
	"harsh.acceleration.event.trigger": model.AlarmAcceleration,
	"harsh.braking.event.trigger":      model.AlarmBraking,
	"harsh.cornering.event.trigger":    model.AlarmCornering,
	"gnss.antenna.cut.status":          model.AlarmGpsAntennaCut,
	"gsm.jamming.event.trigger":        model.AlarmJamming,
	"hood.open.status":                 model.AlarmBonnet,
}

// doubles are the parameters stored as Double attributes.
var doubles = map[string]string{
	"position.hdop":                model.KeyHdop,
	"position.pdop":                model.KeyPdop,
	"gps.vehicle.mileage":          model.KeyOdometer,
	"external.powersource.voltage": model.KeyPower,
	"battery.voltage":              model.KeyBattery,
	"fuel.level":                   model.KeyFuel,
	"can.fuel.level":               model.KeyFuel,
	"engine.rpm":                   model.KeyRpm,
	"can.engine.rpm":               model.KeyRpm,
	"device.temperature":           model.KeyDeviceTemp,
	"custom.wln_accel_max":         "maxAcceleration",
	"custom.wln_brk_max":           "maxBraking",
	"custom.wln_crn_max":           "maxCornering",
}

// ints are the parameters stored as Integer attributes.
var ints = map[string]string{
	"position.satellites": model.KeySatellites,
	"din":                 model.KeyInput,
	"dout":                model.KeyOutput,
	"report.reason":       model.KeyEvent,
	"battery.level":       model.KeyBatteryLevel,
}

func decodeParam(name string, index int, value any, pos *model.Position) bool {
	if alarm, ok := alarms[name]; ok {
		if value == true {
			pos.AddAlarm(alarm)
		}
		return true
	}
	if key, ok := doubles[name]; ok {
		pos.Set(key, number(value).double())
		return true
	}
	if key, ok := ints[name]; ok {
		pos.Set(key, number(value).intValue())
		return true
	}
	switch name {
	case "timestamp":
		pos.SetTime(time.UnixMilli(number(value).longValue() * 1000))
	case "position.latitude":
		pos.SetLatitude(number(value).double())
	case "position.longitude":
		pos.SetLongitude(number(value).double())
	case "position.speed":
		pos.Speed = units.KnotsFromKph(number(value).double())
	case "position.direction":
		pos.Course = number(value).double()
	case "position.altitude":
		pos.Altitude = number(value).double()
	case "position.valid":
		pos.Valid = value == true
	case "can.engine.temperature":
		pos.Set(model.PrefixTemp+strconv.Itoa(int(math.Max(float64(index), 0))), number(value).double())
	case "engine.ignition.status":
		pos.Set(model.KeyIgnition, value == true)
	case "movement.status":
		pos.Set(model.KeyMotion, value == true)
	case "ibutton.code":
		pos.Set(model.KeyDriverUniqueID, str(value))
	case "vehicle.vin":
		pos.Set(model.KeyVin, str(value))
	case "external.powersource.status":
		if value == true {
			pos.Set(model.KeyCharge, true)
		}
	default:
		return false
	}
	return true
}

// decodeUnknownParam keeps other values; numbers end up as Double (Java sets a Long for
// an integral number, then overwrites it with the Double).
func decodeUnknownParam(name string, value any, pos *model.Position) {
	switch v := value.(type) {
	case jsonNumber:
		pos.Set(name, v.double())
	case string:
		pos.Set(name, v)
	case bool:
		pos.Set(name, v)
	}
}
