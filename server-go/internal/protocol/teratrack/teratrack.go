// Package teratrack is the TeraTrack protocol (TeraTrackProtocol): JSON lock reports over
// TCP.
package teratrack

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server of JSON objects.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "teratrack",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.JSONFramer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var dateFormat = kit.NewDateFormat("yyyy-MM-dd HH:mm:ss", time.UTC)

// decode is TeraTrackProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	json := kit.ParseJSONObject([]byte(msg.(string)))

	ds := m.Session(json.String("MDeviceID"), json.String("IMEI"))
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(dateFormat.Parse(json.String("DateTime")))

	pos.Valid = true
	pos.SetLatitude(kit.Double(json.String("Latitude")))
	pos.SetLongitude(kit.Double(json.String("Longitude")))
	pos.Speed = units.KnotsFromKph(float64(kit.Int(json.String("Speed"))))

	pos.Set(model.KeyOdometer, kit.Int(json.String("Mileage")))
	pos.Set(model.KeyLock, json.String("LockOpen") == "0")
	pos.Set(model.KeyDriverUniqueID, json.String("CardNo"))
	if json.String("LowPower") == "1" {
		pos.AddAlarm(model.AlarmLowPower)
	}
	pos.Set(model.KeyBatteryLevel, kit.Int(json.String("Power")))
	pos.Set(model.KeyRssi, kit.Int(json.String("GSM")))

	if m.HasChannel() && json.String("MessageAck") == "1" {
		m.Send("{01}")
	}

	return pos
}
