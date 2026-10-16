// Package manpower is the ManPower protocol (ManPowerProtocol): ';'-delimited "simei:"
// text reports.
package manpower

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "manpower",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ";"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("simei:").
	Number("(d+),"). // imei
	Expression("[^,]*,[^,]*,").
	Expression("([^,]*),"). // status
	Number("d+,d+,d+.?d*,").
	Number("(dd)(dd)(dd)").   // date (yymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Expression("([AV]),").    // validity
	Number("(dd)(dd.dddd),"). // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.dddd),"). // longitude
	Expression("([EW])?,").
	Number("(d+.?d*),"). // speed
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

	pos.Set(model.KeyStatus, parser.NextNullable())
	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	return pos
}
