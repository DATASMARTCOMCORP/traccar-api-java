// Package svias is the Svias protocol (SviasProtocol): ']'-delimited text records over TCP,
// each acknowledged with "@", and text commands.
package svias

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "svias",
		DataCommands: protocol.DataCommands("svias"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "]"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("[").                     // delimiter
	Number("d{4},").               // hardware version
	Number("d{4},").               // software version
	Number("d+,").                 // index
	Number("(d+),").               // imei
	Number("d+,").                 // hour meter
	Number("(d+)(dd)(dd),").       // date (dmmyy)
	Number("(d+)(dd)(dd),").       // time (hmmss)
	Number("(-?)(d+)(dd)(d{5}),"). // latitude
	Number("(-?)(d+)(dd)(d{5}),"). // longitude
	Number("(d+),").               // speed
	Number("(d+),").               // course
	Number("(d+),").               // odometer
	Number("(d+),").               // input
	Number("(d+),").               // output / status
	Number("(d),").
	Number("(d),").
	Number("(d+),"). // power
	Number("(d+),"). // battery level
	Number("(d+),"). // rssi
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	m.Send("@")

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

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMinMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMinMin))
	pos.Speed = units.KnotsFromKph(parser.MustDouble() / 100.0)
	pos.Course = parser.MustDouble() / 100.0
	pos.Set(model.KeyOdometer, parser.MustInt()*100)

	input := parser.MustInt()
	output := parser.MustInt()

	if kit.Check(int64(input), 0) {
		pos.AddAlarm(model.AlarmSos)
	}
	pos.Set(model.KeyIgnition, kit.Check(int64(input), 4))
	pos.Valid = kit.Check(int64(output), 0)

	pos.Set(model.KeyPower, float64(parser.MustInt())/1000.0)
	pos.Set(model.KeyBatteryLevel, parser.MustInt())
	pos.Set(model.KeyRssi, parser.MustInt())

	return pos
}

func encode(m *ingest.Message, c *model.Command) any {
	f := func(format string, keys ...string) any {
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), format, keys...)
	}
	switch c.Type {
	case model.CommandCustom:
		return f("%s", model.CommandKeyData)
	case model.CommandPositionSingle:
		return f("AT+STR=1*")
	case model.CommandSetOdometer:
		return f("AT+ODT=%s*", model.CommandKeyData)
	case model.CommandEngineStop:
		return f("AT+OUT=1,1*")
	case model.CommandEngineResume:
		return f("AT+OUT=1,0*")
	case model.CommandAlarmArm:
		return f("AT+OUT=2,1*")
	case model.CommandAlarmDisarm:
		return f("AT+OUT=2,0*")
	case model.CommandAlarmRemove:
		return f("AT+PNC=600*")
	}
	return nil
}
