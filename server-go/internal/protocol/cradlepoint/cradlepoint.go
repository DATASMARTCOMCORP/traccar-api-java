// Package cradlepoint is the Cradlepoint protocol (CradlepointProtocol): comma-separated
// text lines over TCP.
package cradlepoint

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "cradlepoint",
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
	Expression("([^,]+),").     // id
	Number("(d{1,6}),").        // time (hhmmss)
	Number("(d+)(dd.d+),").     // latitude
	Expression("([NS]),").      //
	Number("(d+)(dd.d+),").     // longitude
	Expression("([EW]),").      //
	Number("(d+.d+)?,").        // speed
	Number("(d+.d+)?,").        // course
	Expression("([^,]+)?,").    // carrier
	Expression("([^,]+)?,").    // serdis
	Number("(-?d+)?,").         // rsrp
	Number("(-?d+)?,").         // rssi
	Number("(-?d+)?,").         // rsrq
	Expression("([^,]+)?,").    // ecio
	Expression("([^,]+)?").     // wan ip
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

	t := parser.MustInt()
	b := kit.NewDateBuilderAt(m.Now, nil)
	b.SetHour(t / 100 / 100)
	b.SetMinute(t / 100 % 100)
	b.SetSecond(t % 100)
	pos.SetTime(b.Date())

	pos.Valid = true
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set("carrid", parser.NextNullable())
	pos.Set("serdis", parser.NextNullable())
	pos.Set("rsrp", parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set("rsrq", parser.NextInt())
	pos.Set("ecio", parser.NextNullable())
	return pos
}
