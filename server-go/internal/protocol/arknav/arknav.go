// Package arknav is the Arknav protocol (ArknavProtocol): comma-separated text reports
// ending with '\r' over TCP, or one per datagram over UDP.
package arknav

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "arknav",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\r"),
					Text:    true,
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

var pattern = kit.NewPattern().
	Number("(d+),").             // imei
	Expression(".{6},").         // id code
	Number("ddd,").              // status
	Number("Lddd,").             // version
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),").     // longitude
	Expression("([EW]),").
	Number("(d+.?d*),").         // speed
	Number("(d+.?d*),").         // course
	Number("(d+.?d*),").         // hdop
	Number("(dd):(dd):(dd) ").   // time (hh:mm:ss)
	Number("(dd)-(dd)-(dd),").   // date (dd-mm-yy)
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

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))
	return pos
}
