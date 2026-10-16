// Package auro is the Auro protocol (AuroProtocol): fixed-width text lines over TCP.
package auro

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "auro",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("M(dddd)").                   // index
	Number("Td+").                       // phone
	Number("I(d+)").                     // imei
	Number("Ed+W").
	Text("*****").
	Number("d{8}d{4}").                  // local time
	Expression(".{8}#.{8}").
	Number("d{10}").                     // status
	Number("([-+])(ddd)(dd)(dddd)").     // longitude
	Number("([-+])(ddd)(dd)(dddd)").     // latitude
	Number("(dd)(dd)(dddd)").            // date (ddmmyyyy)
	Number("(dd)(dd)(dd)").              // time (hhmmss)
	Number("(ddd)").                     // course
	Number("d{6}").
	Number("(ddd)").                     // speed
	Number("d").
	Number("(dd)").                      // battery
	Expression("([01])").                // charging
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.Set(model.KeyIndex, parser.NextIntDefault(0))

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Valid = true
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMinMin))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMinMin))
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))

	pos.Set(model.KeyBattery, parser.NextIntDefault(0))
	pos.Set(model.KeyCharge, parser.NextIntDefault(0) == 1)
	return pos
}
