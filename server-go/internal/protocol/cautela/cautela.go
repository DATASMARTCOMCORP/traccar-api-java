// Package cautela is the Cautela protocol (CautelaProtocol): comma-separated text lines
// over TCP.
package cautela

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "cautela",
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
	Number("(d+),").           // type
	Number("(d+),").           // imei
	Number("(dd),(dd),(dd),"). // date (ddmmyy)
	Number("(-?d+.d+),").      // latitude
	Number("(-?d+.d+),").      // longitude
	Number("(dd)(dd),").       // time (hhmm)
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	parser.Next() // type

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil)
	date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())

	pos.Valid = true
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	date.SetHour(parser.MustInt()).SetMinute(parser.MustInt())
	pos.SetTime(date.Date())

	return pos
}
