// Package homtecs is the Homtecs protocol (HomtecsProtocol): comma-separated text over
// UDP, identified by an id and a MAC ending.
package homtecs

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "homtecs",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Expression("([^_]+)").      // id
	Text("_R").                 //
	Number("(x{8}),").          // mac ending
	Number("(dd)(dd)(dd),").    // date (yymmdd)
	Number("(dd)(dd)(dd).d+,"). // time (hhmmss)
	Number("(d+),").            // satellites
	Number("(dd)(dd.d+),").     // latitude
	Expression("([NS]),").      //
	Number("(ddd)(dd.d+),").    // longitude
	Expression("([EW]),").      //
	Number("(d+.?d*)?,").       // speed
	Number("(d+.?d*)?,").       // course
	Number("(d),").             // fix status
	Number("(d+.?d*)?,").       // hdop
	Number("(d+.?d*)?").        // altitude
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	id := parser.Next()
	mac := parser.Next()
	ds := m.Session(id, id+"_R"+mac)
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.YMD_HMS, nil, m.Now))
	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	pos.Valid = parser.NextIntDefault(0) > 0
	pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))
	pos.Altitude = parser.NextDoubleDefault(0)
	return pos
}
