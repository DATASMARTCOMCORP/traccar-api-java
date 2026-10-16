// Package carcell is the Carcell protocol (CarcellProtocol): comma-separated text reports
// ending with '\r' over TCP. The Java protocol has no test, so there are no recorded calls
// to replay.
package carcell

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
		Name:         "carcell",
		DataCommands: protocol.DataCommands("carcell"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\r"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Expression("([$%])"). // memory flag
	Number("(d+),").      // imei
	GroupBegin().
	Number("([NS])(dd)(dd).(dddd),").  // latitude
	Number("([EW])(ddd)(dd).(dddd),"). // longitude
	Or().
	Text("CEL,").
	Number("([NS])(d+.d+),"). // latitude
	Number("([EW])(d+.d+),"). // longitude
	GroupEnd().
	Number("(d+),"). // speed
	Number("(d+),"). // course
	GroupBegin().
	Number("([-+]ddd)([-+]ddd)([-+]ddd),"). // x,y,z
	Or().
	Number("(d+),"). // accel
	GroupEnd().
	Number("(d+),").         // battery
	Number("(d+),").         // csq
	Number("(d),").          // jamming
	Number("(d+),").         // hdop
	Expression("([CG]),?").  // clock type
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(d),").          // block
	Number("(d),").          // ignition
	GroupBegin().
	Number("(d),").       // cloned
	Expression("([AF])"). // panic
	Number("(d),").       // painel
	Number("(d+),").      // battery voltage
	Or().
	Number("(dd),").      // time until delivery
	Expression("([AF])"). // panic
	Number("(d),").       // aux
	Number("(d{2,4}),").  // battery voltage
	Number("(d{20}),").   // ccid
	GroupEnd().
	Number("(xx)"). // crc
	Any().          // full format
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.Set(model.KeyArchive, parser.Next() == "%")
	pos.Valid = true

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	if parser.HasNextN(8) {
		pos.SetLatitude(parser.NextCoordinate(kit.HemDegMinMin))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDegMinMin))
	}
	if parser.HasNextN(4) {
		pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))
	}

	pos.Speed = units.KnotsFromKph(float64(parser.NextIntDefault(0)))
	pos.Course = float64(parser.NextIntDefault(0))

	if parser.HasNextN(3) {
		pos.Set("x", parser.NextIntDefault(0))
		pos.Set("y", parser.NextIntDefault(0))
		pos.Set("z", parser.NextIntDefault(0))
	}
	if parser.HasNextN(1) {
		pos.Set(model.KeyAcceleration, parser.NextIntDefault(0))
	}

	pos.Set(model.KeyBattery, (parser.NextDoubleDefault(0)+100)*0.0294)
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))
	pos.Set("jamming", parser.Next() == "1")
	pos.Set(model.KeyGps, parser.NextIntDefault(0))

	pos.Set("clockType", parser.Next())

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Set("blocked", parser.Next() == "1")
	pos.Set(model.KeyIgnition, parser.Next() == "1")

	if parser.HasNextN(4) {
		pos.Set("cloned", parser.Next() == "1")

		parser.Next() // panic button status

		painelStatus := parser.Next()
		if painelStatus == "1" {
			pos.AddAlarm(model.AlarmGeneral)
		}
		pos.Set("painel", painelStatus == "2")

		pos.Set(model.KeyPower, parser.NextDoubleDefault(0)/100)
	}

	if parser.HasNextN(5) {
		pos.Set("timeUntilDelivery", parser.NextIntDefault(0))
		parser.Next() // panic button status
		pos.Set(model.KeyInput, parser.Next())

		pos.Set(model.KeyPower, parser.NextDoubleDefault(0)/100)

		pos.Set(model.KeyIccid, parser.Next())
	}

	return pos
}

// encode is CarcellProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "$SRVCMD,%s,BA#\r\n", model.CommandKeyUniqueID)
	case model.CommandEngineResume:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "$SRVCMD,%s,BD#\r\n", model.CommandKeyUniqueID)
	}
	return nil
}
