// Package mobilogix is the Mobilogix protocol (MobilogixProtocol): text messages in
// square brackets, each acknowledged with an "S" reply.
package mobilogix

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
		Name:         "mobilogix",
		DataCommands: protocol.DataCommands("mobilogix"),
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
	Text("[").
	Number("(dddd)-(dd)-(dd) "). // date (yyyymmdd)
	Number("(dd):(dd):(dd),").   // time (hhmmss)
	Number("Td+,").              // type
	Number("(d),").              // archive
	Expression("[^,]+,").        // protocol version
	Expression("([^,]+),").      // serial number
	Number("(xx),").             // status
	Number("(d+.d+)").           // battery
	GroupBegin().
	Text(",").
	Number("(d)").        // satellites
	Number("(d)").        // rssi
	Number("(d),").       // valid
	Number("(-?d+.d+),"). // latitude
	Number("(-?d+.d+),"). // longitude
	Number("(d+.?d*),").  // speed
	Number("(d+.?d*)").   // course
	GroupEndWith("?").
	Any().
	Compile()

func decodeAlarm(t string) string {
	switch t {
	case "T8":
		return model.AlarmLowBattery
	case "T9":
		return model.AlarmVibration
	case "T10":
		return model.AlarmPowerCut
	case "T11":
		return model.AlarmLowPower
	case "T12":
		return model.AlarmGeofenceExit
	case "T13":
		return model.AlarmOverspeed
	case "T15":
		return model.AlarmTow
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))
	t := kit.Substring(sentence, 21, kit.IndexOfFrom(sentence, ",", 21))

	if t != "T6" {
		tm := kit.Substring(sentence, 1, 20)
		if t == "T1" {
			m.Send("[" + tm + ",S1,1]")
		} else {
			m.Send("[" + tm + ",S" + kit.SubstringFrom(t, 1) + "]")
		}
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceTime = model.NewTime(parser.NextDateTimeDefault())
	if parser.MustInt() == 0 {
		pos.Set(model.KeyArchive, true)
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyType, t)
	pos.AddAlarm(decodeAlarm(t))

	status := parser.MustHexInt()
	pos.Set(model.KeyBlocked, !kit.Check(int64(status), 0))
	if kit.Check(int64(status), 1) {
		pos.Set(model.KeyCharge, true)
	}
	pos.Set(model.KeyIgnition, kit.Check(int64(status), 2))
	pos.Set(model.KeyMotion, kit.Check(int64(status), 3))
	pos.Set(model.KeyStatus, status)

	pos.Set(model.KeyBattery, parser.NextDouble())

	if parser.HasNextN(7) {
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyRssi, 6*parser.MustInt()-111)

		pos.Valid = parser.MustInt() > 0
		pos.FixTime = pos.DeviceTime

		pos.SetLatitude(parser.MustDouble())
		pos.SetLongitude(parser.MustDouble())
		pos.Speed = units.KnotsFromKph(parser.MustDouble())
		pos.Course = parser.MustDouble()
	} else {
		dt := pos.DeviceTime.Time
		m.LastLocation(pos, &dt)
	}

	return pos
}

func command(m *ingest.Message, param string) string {
	return "[" + m.Now.In(m.Zone()).Format("2006-01-02 15:04:05") + "," + param + "]"
}

// encode is MobilogixProtocolEncoder; the time is m.Now.
func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			return command(m, "null")
		}
		return command(m, *data)
	case model.CommandEngineResume:
		return command(m, "S6,RELAY=0")
	case model.CommandEngineStop:
		return command(m, "S6,RELAY=1")
	case model.CommandPositionSingle:
		return command(m, "S4,1,1")
	case model.CommandRebootDevice:
		return command(m, "S7")
	}
	return nil
}
