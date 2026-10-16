// Package gt30 is the GT30 protocol (Gt30Protocol): "$$" text lines with an NMEA-like
// position.
package gt30

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gt30",
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
	Text("$$").
	Number("x{4}").                // length
	Expression("(.{14})").         // device id
	Number("x{4}").                // type
	Expression("(.)?").            // alarm
	Number("(dd)(dd)(dd).(ddd),"). // time (hhmmss.sss)
	Expression("([AV]),").         // validity
	Number("(d+)(dd.d+),").        // latitude
	Expression("([NS]),").         //
	Number("(d+)(dd.d+),").        // longitude
	Expression("([EW]),").         //
	Number("(d+.d+)?,").           // speed
	Number("(d+.d+)?,").           // course
	Number("(dd)(dd)(dd)").        // date (ddmmyy)
	Expression(`[^\|]*`).          //
	Number("|(d+.d+)").            // hdop
	Number("|(-?d+)").             // altitude
	Number("x{4}").                // checksum
	Compile()

func decodeAlarm(value rune) string {
	switch value {
	case 0x01, 0x02, 0x03:
		return model.AlarmSos
	case 0x10:
		return model.AlarmLowBattery
	case 0x11:
		return model.AlarmOverspeed
	case 0x12:
		return model.AlarmGeofence
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(kit.Trim(parser.Next()))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNext() {
		pos.AddAlarm(decodeAlarm(kit.CharAt(parser.Next(), 0)))
	}

	b := kit.NewDateBuilder(nil)
	b.SetTimeMillis(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(b.Date())

	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Altitude = parser.NextDoubleDefault(0)
	return pos
}
