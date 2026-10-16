// Package mtx is the MTX protocol (MtxProtocol): "#MTX" text lines, each acknowledged.
package mtx

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "mtx",
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
	Text("#MTX,").
	Number("(d+),").           // imei
	Number("(dddd)(dd)(dd),"). // date (yyyymmdd)
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Number("(-?d+.d+),").      // latitude
	Number("(-?d+.d+),").      // longitude
	Number("(d+.?d*),").       // speed
	Number("(d+),").           // course
	Number("(d+.?d*),").       // odometer
	GroupBegin().
	Number("d+").
	Or().
	Text("X").
	GroupEnd().
	Text(",").
	Expression("(?:[01]|X),").
	Expression("([01]+),"). // input
	Expression("([01]+),"). // output
	Number("(d+),").        // adc1
	Number("(d+)").         // adc2
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	m.Send("#ACK")

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

	pos.Valid = true
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyOdometer, parser.NextDoubleDefault(0)*1000)
	pos.Set(model.KeyInput, parser.Next())
	pos.Set(model.KeyOutput, parser.Next())
	pos.Set(model.PrefixAdc+"1", parser.Next())
	pos.Set(model.PrefixAdc+"2", parser.Next())

	return pos
}
