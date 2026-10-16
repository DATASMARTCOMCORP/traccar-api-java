// Package hunterpro is the Hunter Pro protocol (HunterProProtocol): an identifier and a
// $GPRMC sentence per message, ending with '\r'.
package hunterpro

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "hunterpro",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\r"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number(">(d+)<"). // identifier
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").       //
	Number("(ddd)(dd.d+),").     // longitude
	Expression("([EW]),").       //
	Number("(d+.?d*)?,").        // speed
	Number("(d+.?d*)?,").        // course
	Number("(dd)(dd)(dd)").      // date (ddmmyy)
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

	b := kit.NewDateBuilder(nil)
	hour, minute, second := parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0)
	b.SetTime(hour, minute, second)

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	day, month, year := parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0)
	b.SetDateReverse(day, month, year)
	pos.SetTime(b.Date())

	return pos
}
