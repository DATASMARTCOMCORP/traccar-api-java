// Package tr900 is the TR900 protocol (Tr900Protocol): ">id,..." text lines over TCP and
// UDP.
package tr900

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tr900",
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

var pattern = kit.NewPattern().
	Number(">(d+),").        // id
	Number("d+,").           // period
	Number("(d),").          // fix
	Number("(dd)(dd)(dd),"). // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([EW])").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([NS])").
	Number("(dd)(dd.d+),"). // latitude
	Expression("[^,]*,").   // command
	Number("(d+.?d*),").    // speed
	Number("(d+.?d*),").    // course
	Number("(d+),").        // gsm
	Number("(d+),").        // event
	Number("(d+)-").        // adc
	Number("(d+),").        // battery
	Number("d+,").          // impulses
	Number("(d+),").        // input
	Number("(d+)").         // status
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

	pos.Valid = parser.NextIntDefault(0) == 1

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyRssi, parser.NextDouble())
	pos.Set(model.KeyEvent, parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"1", parser.NextIntDefault(0))
	pos.Set(model.KeyBattery, parser.NextIntDefault(0))
	pos.Set(model.KeyInput, parser.NextNullable())
	pos.Set(model.KeyStatus, parser.NextNullable())

	return pos
}
