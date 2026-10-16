// Package freedom is the Freedom protocol (FreedomProtocol): text lines with the IMEI,
// date, coordinates and speed.
package freedom

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "freedom",
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
	Text("IMEI,").
	Number("(d+),").                 // imei
	Number("(dddd)/(dd)/(dd), ").    // date (yyyy/dd/mm)
	Number("(dd):(dd):(dd), ").      // time (hh:mm:ss)
	Expression("([NS]), ").          //
	Number("Lat:(dd)(d+.d+), ").     // latitude
	Expression("([EW]), ").          //
	Number("Lon:(ddd)(d+.d+), ").    // longitude
	Text("Spd:").Number("(d+.d+)"). // speed
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

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = parser.NextDoubleDefault(0)

	return pos
}
