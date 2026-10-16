// Package fox is the Fox protocol (FoxProtocol): XML elements up to "</fox>" whose id and
// data attributes carry the device and a comma-separated report.
package fox

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "fox",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "</fox>"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("(d+),").          // status id
	Expression("([AV]),").    // validity
	Number("(dd)(dd)(dd),").  // date (ddmmyy)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Number("(dd)(dd.d+),").   // latitude
	Expression("([NS]),").    //
	Number("(ddd)(dd.d+),").  // longitude
	Expression("([EW]),").    //
	Number("(d+.?d*)?,").     // speed
	Number("(d+.?d*)?,").     // course
	Expression("[^,]*,").     // cell info
	Number("([01]+) ").       // input
	Number("(d+) ").          // power
	Number("(d+) ").          // temperature
	Number("(d+) ").          // rpm
	Number("(d+) ").          // fuel
	Number("(d+) ").          // adc 1
	Number("(d+) ").          // adc 2
	Number("([01]+) ").       // output
	Number("(d+),").          // odometer
	Expression("(.+)").       // status info
	Compile()

// attribute finds key="value" in the XML text; nil when it is missing.
func attribute(xml, key string) *string {
	start := kit.IndexOf(xml, key+"=\"")
	if start != -1 {
		start += kit.Len(key) + 2
		end := kit.IndexOfFrom(xml, "\"", start)
		if end != -1 {
			v := kit.Substring(xml, start, end)
			return &v
		}
	}
	return nil
}

func decode(m *ingest.Message, msg any) any {
	xml := msg.(string)
	id := attribute(xml, "id")
	data := attribute(xml, "data")
	if id == nil || data == nil {
		return nil
	}

	ds := m.Session(*id)
	if ds == nil {
		return nil
	}

	parser := kit.NewParser(pattern, *data)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyStatus, parser.NextIntDefault(0))
	pos.Valid = parser.Next() == "A"

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyInput, parser.NextBinIntDefault(0))
	pos.Set(model.KeyPower, parser.NextDoubleDefault(0)/10.0)
	pos.Set(model.PrefixTemp+"1", parser.NextIntDefault(0))
	pos.Set(model.KeyRpm, parser.NextIntDefault(0))
	pos.Set(model.KeyFuel, parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"1", parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"2", parser.NextIntDefault(0))
	pos.Set(model.KeyOutput, parser.NextBinIntDefault(0))
	pos.Set(model.KeyOdometer, parser.NextIntDefault(0))

	pos.Set("statusData", parser.Next())

	return pos
}
