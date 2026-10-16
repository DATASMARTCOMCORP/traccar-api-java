// Package ennfu is the Ennfu protocol (EnnfuProtocol): '$'-delimited text messages over
// TCP.
package ennfu

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "ennfu",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "$"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("Ennfu:").
	Number("(d+),").            // imei
	Number("(dd)(dd)(dd).dd,"). // time (hhmmss)
	Expression("([AV]),").      // validity
	Number("(dd)(dd.d+),").     // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+)?,").     // speed
	Number("(d+.d+)?,").     // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(d+),").         // rssi
	Number("(d+.d+),").      // battery
	Number("(d+.d+),").      // battery level
	Number("(Vd.dd)").       // firmware version
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

	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	b.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(b.Date())

	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyBatteryLevel, parser.NextDouble())
	pos.Set(model.KeyVersionFw, parser.Next())
	return pos
}
