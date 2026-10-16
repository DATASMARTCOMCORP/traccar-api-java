// Package starcom is the Starcom protocol (StarcomProtocol): UDP text messages of
// "|key=value,...|" pairs.
package starcom

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "starcom",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// decode is StarcomProtocolDecoder.decode. The device id stays 0 for an unknown unit.
func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	sentence = kit.Substring(sentence, kit.IndexOf(sentence, "|")+1, kit.LastIndexOf(sentence, "|"))

	pos := m.NewPosition()
	dateFormat := kit.NewDateFormat("yyyy/MM/dd HH:mm:ss", m.Zone())

	for _, entry := range kit.Split(sentence, ",") {
		delimiter := kit.IndexOf(entry, "=")
		key := kit.Substring(entry, 0, delimiter)
		value := kit.SubstringFrom(entry, delimiter+1)
		switch key {
		case "unit":
			if ds := m.Session(value); ds != nil {
				pos.DeviceID = ds.DeviceID
			}
		case "gps_valid":
			pos.Valid = kit.Int(value) != 0
		case "datetime_actual":
			pos.SetTime(dateFormat.Parse(value))
		case "latitude":
			pos.SetLatitude(kit.Double(value))
		case "longitude":
			pos.SetLongitude(kit.Double(value))
		case "altitude":
			pos.Altitude = kit.Double(value)
		case "velocity":
			pos.Speed = units.KnotsFromKph(float64(kit.Int(value)))
		case "heading":
			pos.Course = float64(kit.Int(value))
		case "eventid":
			pos.Set(model.KeyEvent, kit.Int(value))
		case "odometer":
			pos.Set(model.KeyOdometer, int64(kit.Double(value)*1000))
		case "satellites":
			pos.Set(model.KeySatellites, kit.Int(value))
		case "ignition":
			pos.Set(model.KeyIgnition, kit.Int(value) != 0)
		case "door":
			pos.Set(model.KeyDoor, kit.Int(value) != 0)
		case "arm":
			pos.Set(model.KeyArmed, kit.Int(value) != 0)
		case "fuel":
			pos.Set(model.KeyFuel, kit.Int(value))
		case "rpm":
			pos.Set(model.KeyRpm, kit.Int(value))
		case "main_voltage":
			pos.Set(model.KeyPower, kit.Double(value))
		case "backup_voltage":
			pos.Set(model.KeyBattery, kit.Double(value))
		case "analog1", "analog2", "analog3":
			pos.Set(model.PrefixAdc+kit.SubstringFrom(key, kit.Len(key)-1), kit.Double(value))
		default:
			pos.Set(key, value)
		}
	}

	return pos
}
