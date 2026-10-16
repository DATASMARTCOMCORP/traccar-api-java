// Package pt3000 is the PT3000 protocol (Pt3000Protocol): "%imei,$GPRMC,..." text
// frames ended by 'd'.
package pt3000

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "pt3000",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "d"), // "probably wrong", as Java says
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("%(d+),"). // imei
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").   // speed
	Number("(d+.?d*)?,").   // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).
		SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())

	return pos
}
