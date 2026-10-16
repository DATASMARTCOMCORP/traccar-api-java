// Package swiftech is the Swiftech protocol (SwiftechProtocol): '#'-delimited "@@" text
// records over TCP.
package swiftech

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "swiftech",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "#"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("@@").
	Number("(d+),"). // imei
	Expression("[^,]*,").
	Expression("[^,]*,").
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(dd)(dd.d+),").  // latitude
	Expression("([NS]),").
	Number("(d{2,3})(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+),").      // speed
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Expression("([AV]),").   // validity
	Number("(d{4}),").       // status
	Number("([01]),").       // charge
	Number("d+,").           // reserved
	Number("(d+),").         // adc1
	Number("(d+),").         // adc2
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.MustDouble()
	date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(date.Date())
	pos.Valid = parser.Next() == "A"

	pos.Set(model.KeyStatus, parser.MustInt())
	pos.Set(model.KeyCharge, parser.MustInt() > 0)
	pos.Set(model.PrefixAdc+"1", float64(parser.MustInt())/1000.0)
	pos.Set(model.PrefixAdc+"2", float64(parser.MustInt())/1000.0)

	return pos
}
