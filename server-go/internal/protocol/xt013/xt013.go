// Package xt013 is the Xt013 protocol (Xt013Protocol): "TK,..." text lines over TCP.
package xt013

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "xt013",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(Decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("HI,d+").Optional().
	Text("TK,").
	Number("(d+),").         // imei
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("([+-]d+.d+),").  // latitude
	Number("([+-]d+.d+),").  // longitude
	Number("(d+),").         // speed
	Number("(d+),").         // course
	Number("d+,").
	Number("(d+),").       // altitude
	Expression("([FL]),"). // gps fix
	Number("d+,").
	Number("(d+),"). // gps level
	Number("x+,").
	Number("x+,").
	Number("(d+),"). // gsm level
	Expression("[^,]*,").
	Number("(d+.d+),"). // battery
	Number("(d),").     // charging
	Any().
	Compile()

// Decode is Xt013ProtocolDecoder.
func Decode(m *ingest.Message, msg any) any {
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

	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Valid = parser.Next() == "F"

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextDouble())
	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0))
	pos.Set(model.KeyCharge, parser.Next() == "1")

	return pos
}
