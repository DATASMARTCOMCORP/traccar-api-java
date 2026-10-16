// Package topflytech is the Topflytech protocol (TopflytechProtocol): "(imei...)" text
// messages delimited by ')'.
package topflytech

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "topflytech",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ")"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("(").
	Number("(d+)"). // imei
	Any().
	Number("(dd)(dd)(dd)"). // date (yymmdd)
	Number("(dd)(dd)(dd)"). // time (hhmmss)
	Expression("([AV])").
	Number("(dd)(dd.dddd)([NS])").  // latitude
	Number("(ddd)(dd.dddd)([EW])"). // longitude
	Number("(ddd.d)").              // speed
	Number("(d+.d+)").              // course
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

	pos.SetTime(parser.NextDateTimeDefault())

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	return pos
}
