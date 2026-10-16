// Package kenji is the Kenji protocol (KenjiProtocol): '>C...' text lines over TCP.
package kenji

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "kenji",
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
	Text(">").
	Number("C(d{6}),").            // device id
	Number("M(x{6}),").            // alarm
	Number("O(x{4}),").            // output
	Number("I(x{4}),").            // input
	Number("D(dd)(dd)(dd),").      // time (hhmmss)
	Expression("([AV]),").         // valid
	Number("([NS])(dd)(dd.d+),").  // latitude
	Number("([EW])(ddd)(dd.d+),"). // longitude
	Number("T(d+.d+),").           // speed
	Number("H(d+.d+),").           // course
	Number("Y(dd)(dd)(dd),").      // date (ddmmyy)
	Number("G(d+)").               // satellites
	Any().
	Compile()

func decodeAlarm(value int) string {
	v := int64(value)
	switch {
	case kit.Check(v, 2):
		return model.AlarmSos
	case kit.Check(v, 4):
		return model.AlarmLowBattery
	case kit.Check(v, 6):
		return model.AlarmMovement
	case kit.Check(v, 1) || kit.Check(v, 10) || kit.Check(v, 11):
		return model.AlarmVibration
	}
	return ""
}

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

	pos.AddAlarm(decodeAlarm(parser.NextHexIntDefault(0)))
	pos.Set(model.KeyOutput, parser.NextHexIntDefault(0))
	pos.Set(model.KeyInput, parser.NextHexIntDefault(0))

	b := kit.NewDateBuilder(nil).SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"

	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(b.Date())

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	return pos
}
