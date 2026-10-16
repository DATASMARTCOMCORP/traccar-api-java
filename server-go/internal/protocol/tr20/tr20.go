// Package tr20 is the TR20 protocol (Tr20Protocol): "%%" text lines over TCP and UDP,
// pings answered with "&&".
package tr20

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tr20",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
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

var patternPing = kit.NewPattern().
	Text("%%").
	Expression("[^,]+,").
	Number("(d+)").
	Compile()

var patternData = kit.NewPattern().
	Text("%%").
	Expression("([^,]+),").  // id
	Expression("([AL]),").   // validity
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([NS])").
	Number("(dd)(dd.d+)"). // latitude
	Expression("([EW])").
	Number("(ddd)(dd.d+),").             // longitude
	Number("(d+),").                     // speed
	Number("(d+),").                     // course
	Number("(?:NA|[BFC]?(-?d+)[^,]*),"). // temperature
	Number("(x{8}),").                   // status
	Number("(d+)").                      // event
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(patternPing, msg.(string))
	if parser.Matches() {
		m.Send("&&" + parser.Next() + "\r\n") // keep-alive response
		return nil
	}

	parser = kit.NewParser(patternData, msg.(string))
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

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()

	pos.Set(model.PrefixTemp+"1", parser.NextInt())
	pos.Set(model.KeyStatus, parser.NextHexLong())
	pos.Set(model.KeyEvent, parser.NextInt())

	return pos
}
