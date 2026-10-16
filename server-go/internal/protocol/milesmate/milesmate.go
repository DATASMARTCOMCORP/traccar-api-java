// Package milesmate is the Milesmate protocol (MilesmateProtocol): "ApiString={...}" text
// lines, acknowledged.
package milesmate

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server of lines.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "milesmate",
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
	Text("ApiString={").
	Number("A:(d+),").               // imei
	Number("B:(d+.d+),").            // battery
	Number("C:(d+.d+),").            // adc
	Number("D:(dd)(dd)(dd),").       // time (hhmmss)
	Number("E:(dd)(dd.d+)([NS]),").  // latitude
	Number("F:(ddd)(dd.d+)([EW]),"). // longitude
	Number("G:(d+.d+),").            // speed
	Number("H:(dd)(dd)(dd),").       // date (ddmmyy)
	Expression("I:[GL],").           // location source
	Number("J:(d{8}),").             // flags
	Number("K:(d{7})").              // flags
	Expression("([AV]),").           // validity
	Number("L:d{4},").               // pin
	Number("M:(d+.d+)").             // course
	Text("}").
	Compile()

func alarmIf(condition bool, alarm string) string {
	if condition {
		return alarm
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	m.Send("+##Received OK\n")

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.PrefixAdc+"1", parser.NextDouble())

	date := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = units.KnotsFromKph(parser.MustDouble())

	date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(date.Date())

	flags := parser.Next()
	pos.Set(model.KeyIgnition, kit.CharAt(flags, 0) == '1')
	pos.AddAlarm(alarmIf(kit.CharAt(flags, 1) == '1', model.AlarmSos))
	pos.Set(model.KeyCharge, kit.CharAt(flags, 5) == '1')
	pos.AddAlarm(alarmIf(kit.CharAt(flags, 7) == '1', model.AlarmOverspeed))

	flags = parser.Next()
	pos.Set(model.KeyBlocked, kit.CharAt(flags, 0) == '1')
	pos.AddAlarm(alarmIf(kit.CharAt(flags, 1) == '1', model.AlarmTow))

	pos.Valid = parser.Next() == "A"
	pos.Course = parser.MustDouble()
	return pos
}
