// Package owntracks is the OwnTracks protocol (OwnTracksProtocol): JSON location messages
// posted over HTTP.
package owntracks

import (
	"context"
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
	return ingest.Protocol{Name: "owntracks", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

// Decoder is OwnTracksProtocolDecoder.
type Decoder struct{}

var (
	ok         = &ingest.HTTPResponse{Status: http.StatusOK}
	badRequest = &ingest.HTTPResponse{Status: http.StatusBadRequest}
)

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
	root := kit.ParseJSONObject([]byte(kit.Latin1(body))) // toString(US_ASCII)

	if root.String("_type") != "location" {
		return ok, nil, nil
	}

	ds := m.Session(root.String("tid"))
	if ds == nil {
		return badRequest, nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(time.UnixMilli(number(root, "tst").Long() * 1000))
	if root.Has("sent") {
		pos.DeviceTime = model.NewTime(time.UnixMilli(number(root, "sent").Long() * 1000))
	}

	pos.Valid = true

	pos.SetLatitude(number(root, "lat").Double())
	pos.SetLongitude(number(root, "lon").Double())

	if root.Has("vel") {
		pos.Speed = units.KnotsFromKph(float64(root.Int("vel")))
	}
	if root.Has("alt") {
		pos.Altitude = float64(root.Int("alt"))
	}
	if root.Has("cog") {
		pos.Course = float64(root.Int("cog"))
	}
	if root.Has("acc") {
		pos.Accuracy = float64(root.Int("acc"))
	}
	if root.Has("t") {
		event := root.String("t")
		decodeAlarm(pos, event, root.IntOr("rty", -1))
		pos.Set(model.KeyEvent, event)
	}
	if root.Has("batt") {
		pos.Set(model.KeyBatteryLevel, root.Int("batt"))
	}
	if root.Has("uext") {
		pos.Set(model.KeyPower, number(root, "uext").Double())
	}
	if root.Has("ubatt") {
		pos.Set(model.KeyBattery, number(root, "ubatt").Double())
	}
	if root.Has("vin") {
		pos.Set(model.KeyVin, root.String("vin"))
	}
	if root.Has("name") {
		pos.Set(model.KeyVin, root.String("name"))
	}
	if root.Has("rpm") {
		pos.Set(model.KeyRpm, root.Int("rpm"))
	}
	if root.Has("ign") {
		pos.Set(model.KeyIgnition, root.Bool("ign"))
	}
	if root.Has("motion") {
		pos.Set(model.KeyMotion, root.Bool("motion"))
	}
	if root.Has("odometer") {
		pos.Set(model.KeyOdometer, number(root, "odometer").Double()*1000.0)
	}
	if root.Has("hmc") {
		pos.Set(model.KeyHours, number(root, "hmc").Double()/3600.0)
	}

	if root.Has("anum") {
		inputCount := root.Int("anum")
		for i := range inputCount {
			index := kit.Format("%02d", i)
			if root.Has("adda-" + index) {
				pos.Set(model.PrefixAdc+strconv.Itoa(i+1), root.String("adda-"+index))
			}
			if root.Has("temp_c-" + index) {
				pos.Set(model.PrefixTemp+strconv.Itoa(i+1), number(root, "temp_c-"+index).Double())
			}
		}
	}

	return ok, []*model.Position{pos}, nil
}

func decodeAlarm(pos *model.Position, event string, reportType int) {
	switch event {
	case "9":
		pos.AddAlarm(model.AlarmLowBattery)
	case "1":
		pos.AddAlarm(model.AlarmPowerOn)
	case "i":
		pos.Set(model.KeyIgnition, true)
	case "I":
		pos.Set(model.KeyIgnition, false)
	case "E":
		pos.AddAlarm(model.AlarmPowerRestored)
	case "e":
		pos.AddAlarm(model.AlarmPowerCut)
	case "!":
		pos.AddAlarm(model.AlarmTow)
	case "s":
		pos.AddAlarm(model.AlarmOverspeed)
	case "h":
		switch reportType {
		case 0, 3:
			pos.AddAlarm(model.AlarmBraking)
		case 1, 4:
			pos.AddAlarm(model.AlarmAcceleration)
		case 2, 5:
			pos.AddAlarm(model.AlarmCornering)
		}
	}
}
