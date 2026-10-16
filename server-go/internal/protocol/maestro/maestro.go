// Package maestro is the Maestro protocol (MaestroProtocol): fixed 160-byte text reports.
package maestro

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "maestro",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.FixedFramer{Length: 160},
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("@").
	Number("(d+),").            // imei
	Number("d+,").              // index
	Expression("[^,]+,").       // profile
	Expression("([01]),").      // validity
	Number("(d+.d+),").         // battery
	Number("(d+),").            // gsm
	Expression("([01]),").      // starter
	Expression("([01]),").      // ignition
	Number("(dd)/(dd)/(dd),").  // date (yy/mm/dd)
	Number("(dd):(dd):(dd),").  // time (hh:mm:ss)
	Number("(-?d+.d+),").       // longitude
	Number("(-?d+.d+),").       // latitude
	Number("(d+.?d*),").        // altitude
	Number("(d+.?d*),").        // speed
	Number("(d+.?d*),").        // course
	Number("(d+),").            // satellites
	Number("(d+.?d*),").        // hdop
	Number("(d+.?d*)").         // odometer
	Number(",(d+)").Optional(). // adc
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Valid = parser.NextIntDefault(0) == 1
	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0))
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))
	pos.Set(model.KeyCharge, parser.NextIntDefault(0) == 1)
	pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)

	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Speed = units.KnotsFromMph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))
	pos.Set(model.KeyOdometer, parser.NextDoubleDefault(0)*1609.34)

	if parser.HasNext() {
		pos.Set(model.PrefixAdc+"1", parser.NextIntDefault(0))
	}
	return pos
}
