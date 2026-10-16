// Package easytrack is the EasyTrack protocol (EasyTrackProtocol): "*ET,..." text
// messages with hex fields, over TCP.
package easytrack

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
		Name:         "easytrack",
		DataCommands: protocol.DataCommands("easytrack"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "#\r\n", "#", "\r\n"),
				Text:    true,
				TextOut: true,
				Decoder: &Decoder{},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("*").Expression("..,"). // manufacturer
	Number("d+,").               // imei
	Expression("([^,]{2}),").    // command
	Expression("([AV]),").       // validity
	Number("(xx)(xx)(xx),").     // date (yymmdd)
	Number("(xx)(xx)(xx),").     // time (hhmmss)
	Number("(x)(x{7}),").        // latitude
	Number("(x)(x{7}),").        // longitude
	Number("(x{4}),").           // speed
	Number("(x{4}),").           // course
	Number("(x{8}),").           // status
	Number("(x+),").             // signal
	Number("(d+),").             // power
	Number("(x+),").             // fuel / index
	Number("(x+)").              // odometer
	GroupBegin().
	Number(",(x+)"). // altitude
	GroupBegin().
	Number(",d+").      // gps data
	Number(",(x*)").    // rfid
	Number(",(x+)").    // temperature
	Number(",(d+.d+)"). // adc
	Number(",(d+)").    // satellites
	GroupEndWith("?").
	GroupEndWith("?").
	Any().
	Compile()

var patternCell = kit.NewPattern().
	Text("*").Expression("..,"). // manufacturer
	Number("d+,").               // imei
	Text("JZ,").                 // command
	Number("[01],").             // result
	Number("(d+),").             // cid
	Number("(d+),").             // lac
	Number("(d+),").             // mcc
	Number("(d+)").              // mnc
	Any().
	Compile()

var patternOBD = kit.NewPattern().
	Text("*").Expression("..,"). // manufacturer
	Number("d+,").               // imei
	Text("OB,").                 // command
	Text("BD$").
	Number("V(d+.d);"). // battery
	Number("R(d+);").   // rpm
	Number("S(d+);").   // speed
	Number("P(d+.d);"). // throttle
	Number("O(d+.d);"). // engine load
	Number("C(d+);").   // coolant temperature
	Number("L(d+.d);"). // fuel level
	Number("[XY][MH]d+.d+;").
	Number("Md+.?d*;").  // mileage
	Number("F(d+.d+);"). // fuel consumption
	Number("T(d+);").    // engine time
	Any().
	Compile()

var ackTypes = map[string]bool{"HB": true, "CC": true, "AM": true, "DW": true, "JZ": true}

// Decoder is EasyTrackProtocolDecoder. ModelOverride replaces the device model
// (setModelOverride).
type Decoder struct {
	ModelOverride string
}

func (d *Decoder) deviceModel(m *ingest.Message, ds *ingest.DeviceSession) string {
	if d.ModelOverride != "" {
		return d.ModelOverride
	}
	return m.DeviceModel(ds)
}

func decodeAlarm(pos *model.Position, status int64, deviceModel string) {
	if status&0x02000000 != 0 {
		pos.AddAlarm(model.AlarmGeofenceEnter)
	}
	if status&0x04000000 != 0 {
		pos.AddAlarm(model.AlarmGeofenceExit)
	}
	if status&0x08000000 != 0 {
		pos.AddAlarm(model.AlarmLowBattery)
	}
	if status&0x10000000 != 0 || status&0x00000008 != 0 {
		pos.AddAlarm(model.AlarmJamming)
	}
	if status&0x20000000 != 0 {
		pos.AddAlarm(model.AlarmVibration)
	}
	if status&0x80000000 != 0 {
		pos.AddAlarm(model.AlarmOverspeed)
	}
	if status&0x00010000 != 0 || status&0x00000400 != 0 {
		pos.AddAlarm(model.AlarmSos)
	}
	if status&0x00040000 != 0 {
		if deviceModel == "E3+4G" {
			pos.AddAlarm(model.AlarmTampering)
		} else {
			pos.AddAlarm(model.AlarmPowerCut)
		}
	}
	if status&0x00040000 != 0 {
		pos.AddAlarm(model.AlarmPowerCut)
	}
	if status&0x00004000 != 0 {
		pos.AddAlarm(model.AlarmLowPower)
	}
	if status&0x00008000 != 0 {
		pos.AddAlarm(model.AlarmTemperature)
	}
	if status&0x00000100 != 0 {
		pos.AddAlarm(model.AlarmRemoving)
	}
	if status&0x00000001 != 0 {
		pos.AddAlarm(model.AlarmBraking)
	}
	if status&0x00000002 != 0 {
		pos.AddAlarm(model.AlarmAcceleration)
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	typeIndex := kit.IndexOfFrom(sentence, ",", 4) + 1
	imei := kit.Substring(sentence, 4, typeIndex-1)
	typ := kit.Substring(sentence, typeIndex, typeIndex+2)

	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	if m.HasChannel() {
		if typ == "TX" || typ == "MQ" {
			m.Send(sentence + "#")
		} else if d.deviceModel(m, ds) == "E3+4G" && ackTypes[typ] {
			m.Send(kit.Substring(sentence, 0, typeIndex+3) + "ACK#")
		}
	}

	switch typ {
	case "OB":
		return decodeOBD(m, ds, sentence)
	case "JZ":
		if m.HasChannel() && kit.Int(kit.Substring(sentence, typeIndex+3, typeIndex+4)) > 0 {
			m.Send("*ET," + imei + ",JZ,undefined#")
		}
		return decodeCell(m, ds, sentence)
	}
	return d.decodeLocation(m, ds, sentence)
}

func (d *Decoder) decodeLocation(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	deviceModel := d.deviceModel(m, ds)

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyCommand, parser.Next())
	pos.Valid = parser.Next() == "A"

	b := kit.NewDateBuilder(nil)
	b.SetDate(parser.MustHexInt(), parser.MustHexInt(), parser.MustHexInt())
	b.SetTime(parser.MustHexInt(), parser.MustHexInt(), parser.MustHexInt())
	pos.SetTime(b.Date())

	if kit.Check(int64(parser.MustHexInt()), 3) {
		pos.SetLatitude(float64(-parser.MustHexInt()) / 600000.0)
	} else {
		pos.SetLatitude(float64(parser.MustHexInt()) / 600000.0)
	}
	if kit.Check(int64(parser.MustHexInt()), 3) {
		pos.SetLongitude(float64(-parser.MustHexInt()) / 600000.0)
	} else {
		pos.SetLongitude(float64(parser.MustHexInt()) / 600000.0)
	}

	pos.Speed = units.KnotsFromKph(float64(parser.MustHexInt()) / 100.0)
	if course := float64(parser.MustHexInt()) / 100.0; course < 360 {
		pos.Course = course
	}

	status := parser.MustHexLong()
	decodeAlarm(pos, status, deviceModel)
	pos.Set(model.KeyBlocked, status&0x00080000 > 0)
	pos.Set(model.KeyIgnition, status&0x00800000 > 0)
	pos.Set(model.KeyStatus, status)

	pos.Set(model.KeyRssi, parser.NextHexInt())
	pos.Set(model.KeyPower, parser.NextDouble())

	if deviceModel == "E3+4G" {
		pos.Set(model.KeyIndex, parser.NextHexInt())
	} else {
		pos.Set(model.KeyFuel, parser.NextHexInt())
	}

	pos.Set(model.KeyOdometer, int(int32(parser.MustHexInt()*100)))

	pos.Altitude = parser.NextDoubleDefault(0)

	if parser.HasNextN(4) {
		if deviceModel == "E3+4G" {
			pos.Set(model.KeyHours, parser.MustHexLong()*60000)
		} else {
			pos.Set(model.KeyDriverUniqueID, parser.NextNullable())
		}
		pos.Set(model.PrefixTemp+"1", float64(parser.MustHexInt())/100.0)
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.KeySatellites, parser.NextInt())
	}
	return pos
}

func decodeCell(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternCell, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	cid := parser.MustInt()
	lac := parser.MustInt()
	mcc := parser.MustInt()
	mnc := parser.MustInt()
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	return pos
}

func decodeOBD(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternOBD, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyRpm, parser.NextInt())
	pos.Set(model.KeyObdSpeed, parser.NextInt())
	pos.Set(model.KeyThrottle, parser.NextDouble())
	pos.Set(model.KeyEngineLoad, parser.NextDouble())
	pos.Set(model.KeyCoolantTemp, parser.NextInt())
	pos.Set(model.KeyFuel, parser.NextDouble())
	pos.Set(model.KeyFuelConsumption, parser.NextDouble())
	pos.Set(model.KeyHours, parser.NextInt())
	return pos
}

// encode is EasyTrackProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	id := m.UniqueID(c.DeviceID)
	switch c.Type {
	case model.CommandCustom:
		return kit.FormatCommand(c, id, "*ET,%s,KS,%s#", model.CommandKeyUniqueID, model.CommandKeyData)
	case model.CommandEngineStop:
		return kit.FormatCommand(c, id, "*ET,%s,FD,Y1#", model.CommandKeyUniqueID)
	case model.CommandEngineResume:
		return kit.FormatCommand(c, id, "*ET,%s,FD,Y2#", model.CommandKeyUniqueID)
	case model.CommandAlarmArm:
		return kit.FormatCommand(c, id, "*ET,%s,FD,F1#", model.CommandKeyUniqueID)
	case model.CommandAlarmDisarm:
		return kit.FormatCommand(c, id, "*ET,%s,FD,F2#", model.CommandKeyUniqueID)
	}
	return nil
}
