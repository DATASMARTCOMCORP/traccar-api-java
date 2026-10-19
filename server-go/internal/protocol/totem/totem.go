// Package totem is the Totem protocol (TotemProtocol): "$$" text frames carrying their
// length, in several generations of formats.
package totem

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server. The SMS (text channel) encoder,
// TotemProtocolSmsEncoder, is SMSEncoder.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "totem",
		DataCommands: protocol.DataCommands("totem"),
		TextEncoder:  SMSEncoder,
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Framer is TotemFrameDecoder: from "$$", a length of four decimal digits (when the
// first is '0') or two hex digits.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}

		begin := buf.IndexOfIn("$$", buf.ReaderIndex(), buf.WriterIndex())
		if begin == -1 {
			return nil
		} else if begin > buf.ReaderIndex() {
			buf.SetReaderIndex(begin)
		}

		var length int
		if buf.GetByte(buf.ReaderIndex()+2) == '0' {
			length = kit.Int(buf.GetString(buf.ReaderIndex()+2, 4))
		} else {
			length = kit.IntRadix(buf.GetString(buf.ReaderIndex()+2, 2), 16)
		}

		if length <= buf.ReadableBytes() {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

var pattern1 = kit.NewPattern().
	Text("$$").         // header
	Number("xx").       // length
	Number("(d+)|").    // imei
	Expression("(..)"). // alarm
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).d+,").    // time (hhmmss)
	Expression("([AV]),").         // validity
	Number("(d+)(dd.d+),([NS]),"). // latitude
	Number("(d+)(dd.d+),([EW]),"). // longitude
	Number("(d+.?d*)?,").          // speed
	Number("(d+.?d*)?,").          // course
	Number("(dd)(dd)(dd)").        // date (ddmmyy)
	Expression("[^*]*").Text("*").
	Number("xx|").              // checksum
	Number("(d+.d+)|").         // pdop
	Number("(d+.d+)|").         // hdop
	Number("(d+.d+)|").         // vdop
	Number("(d+)|").            // io status
	Number("d+|").              // battery time
	Number("d").                // charged
	Number("(ddd)").            // battery
	Number("(dddd)|").          // power
	Number("(d+)|").Optional(). // adc
	Number("x*(xxxx)").         // lac
	Number("(xxxx)|").          // cid
	Number("(d+)|").            // temperature
	Number("(d+.d+)|").         // odometer
	Number("d+|").              // serial number
	Any().
	Number("xxxx"). // checksum
	Any().
	Compile()

var pattern2 = kit.NewPattern().
	Text("$$").              // header
	Number("xx").            // length
	Number("(d+)|").         // imei
	Expression("(..)").      // alarm type
	Number("(dd)(dd)(dd)").  // date (ddmmyy)
	Number("(dd)(dd)(dd)|"). // time (hhmmss)
	Expression("([AV])|").   // validity
	Number("(d+)(dd.d+)|").  // latitude
	Expression("([NS])|").
	Number("(d+)(dd.d+)|"). // longitude
	Expression("([EW])|").
	Number("(d+.d+)?|"). // speed
	Number("(d+)?|").    // course
	Number("(d+.d+)|").  // hdop
	Number("(d+)|").     // io status
	Number("d").         // charged
	Number("(dd)").      // battery
	Number("(dd)|").     // external power
	Number("(d+)|").     // adc
	Number("(xxxx)").    // lac
	Number("(xxxx)|").   // cid
	Number("(d+)|").     // temperature
	Number("(d+.d+)|").  // odometer
	Number("d+|").       // serial number
	Number("xxxx").      // checksum
	Any().
	Compile()

var pattern3 = kit.NewPattern().
	Text("$$").                     // header
	Number("xx").                   // length
	Number("(d+)|").                // imei
	Expression("(..)").             // alarm type
	Number("(dd)(dd)(dd)").         // date (ddmmyy)
	Number("(dd)(dd)(dd)").         // time (hhmmss)
	Number("(xxxx)").               // io status
	Expression("[01]").             // charging
	Number("(dd)").                 // battery
	Number("(dd)").                 // external power
	Number("(dddd)").               // adc 1
	Number("(dddd)").               // adc 2
	Number("(ddd)").                // temperature 1
	Number("(ddd)").                // temperature 2
	Number("(xxxx)").               // lac
	Number("(xxxx)").               // cid
	Expression("([AV])").           // validity
	Number("(dd)").                 // satellites
	Number("(ddd)").                // course
	Number("(ddd)").                // speed
	Number("(dd.d)").               // pdop
	Number("(d{7})").               // odometer
	Number("(dd)(dd.dddd)([NS])").  // latitude
	Number("(ddd)(dd.dddd)([EW])"). // longitude
	Number("dddd").                 // serial number
	Number("xxxx").                 // checksum
	Any().
	Compile()

var pattern4 = kit.NewPattern().
	Text("$$").             // header
	Number("dddd").         // length
	Number("xx").           // type
	Number("(d+)|").        // imei
	Number("(x{8})").       // status
	Number("(dd)(dd)(dd)"). // date (yymmdd)
	Number("(dd)(dd)(dd)"). // time (hhmmss)
	GroupBegin().
	Number("(dd)"). // battery
	Number("(dd)"). // external power
	Or().
	Number("(ddd)"). // battery
	GroupEnd().
	Number("(dddd)"). // adc 1
	GroupBegin().
	GroupBegin().
	Number("(dddd)"). // adc 2
	Number("(dddd)"). // adc 3
	Number("(dddd)"). // adc 4
	GroupEndWith("?").
	Number("(dddd)").  // temperature 1
	Number("(dddd)?"). // temperature 2
	GroupEndWith("?").
	Number("(xxxx)"). // lac
	Number("(xxxx)"). // cid
	GroupBegin().
	Number("(dd)").  // mcc
	Number("(ddd)"). // mnc
	GroupEndWith("?").
	Number("(dd)").                 // satellites
	Number("(dd)").                 // gsm (rssi)
	Number("(ddd)").                // course
	Number("(ddd)").                // speed
	Number("(dd.d)").               // hdop
	Number("(d{7})").               // odometer
	Number("(dd)(dd.dddd)([NS])").  // latitude
	Number("(ddd)(dd.dddd)([EW])"). // longitude
	Number("dddd").Optional().      // temperature
	Number("dddd").                 // serial number
	Number("xx").                   // checksum
	Any().
	Compile()

var patternE2 = kit.NewPattern().
	Text("$$").              // header
	Number("dddd").          // length
	Number("xx").            // type
	Number("(d+)|").         // imei
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+.d+),").    // latitude
	Expression("(.+)").      // rfid
	Number("|xx").           // checksum
	Any().
	Compile()

var patternE5 = kit.NewPattern().
	Text("$$").              // header
	Number("dddd").          // length
	Number("xx").            // type
	Number("(d+)|").         // imei
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+.d+),").    // latitude
	Expression("[^,]*,").    // obd version
	Number("(d+),").         // odometer
	Number("(d+),").         // fuel used
	Number("(d+),").         // fuel consumption
	Number("(d+),").         // power
	Number("(d+),").         // rpm
	Number("(d+),").         // speed
	Number("(d+),").         // intake flow
	Number("(d+),").         // intake pressure
	Number("(d+),").         // coolant temperature
	Number("(d+),").         // intake temperature
	Number("(d+),").         // engine load
	Number("(d+),").         // throttle
	Number("(d+),").         // fuel
	Number("|xx").           // checksum
	Any().
	Compile()

func decodeAlarm123(value int) string {
	switch value {
	case 0x01:
		return model.AlarmSos
	case 0x10:
		return model.AlarmLowBattery
	case 0x11:
		return model.AlarmOverspeed
	case 0x30:
		return model.AlarmParking
	case 0x42:
		return model.AlarmGeofenceExit
	case 0x43:
		return model.AlarmGeofenceEnter
	}
	return ""
}

func decodeAlarm4(value int) string {
	switch value {
	case 0x01:
		return model.AlarmSos
	case 0x02:
		return model.AlarmOverspeed
	case 0x04:
		return model.AlarmGeofenceExit
	case 0x05:
		return model.AlarmGeofenceEnter
	case 0x06:
		return model.AlarmTow
	case 0x07:
		return model.AlarmGpsAntennaCut
	case 0x10:
		return model.AlarmPowerCut
	case 0x11:
		return model.AlarmPowerRestored
	case 0x12:
		return model.AlarmLowPower
	case 0x13:
		return model.AlarmLowBattery
	case 0x40:
		return model.AlarmVibration
	case 0x41:
		return model.AlarmIdle
	case 0x42:
		return model.AlarmAcceleration
	case 0x43:
		return model.AlarmBraking
	}
	return ""
}

// parseShortHex is Short.parseShort(s, 16).
func parseShortHex(s string) int {
	v := kit.IntRadix(s, 16)
	if v < -32768 || v > 32767 {
		kit.Throw("NumberFormatException: Value out of range. Value:%q Radix:16", s)
	}
	return v
}

// mustBin is the unboxed parser.nextBinInt(): a missing group fails.
func mustBin(parser *kit.Parser) int {
	v := parser.NextBinInt()
	if v == nil {
		kit.Throw("NullPointerException: the group didn't match")
	}
	return *v
}

func alarmIf(condition bool, alarm string) string {
	if condition {
		return alarm
	}
	return ""
}

func decode12(m *ingest.Message, sentence string, first bool) *model.Position {
	p := pattern2
	if first {
		p = pattern1
	}
	parser := kit.NewParser(p, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNext() {
		pos.AddAlarm(decodeAlarm123(parseShortHex(parser.Next())))
	}
	db := kit.NewDateBuilder(nil)
	var year, month, day int
	if !first {
		day = parser.NextIntDefault(0)
		month = parser.NextIntDefault(0)
		year = parser.NextIntDefault(0)
	}
	db.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	if first {
		day = parser.NextIntDefault(0)
		month = parser.NextIntDefault(0)
		year = parser.NextIntDefault(0)
	}
	if year == 0 {
		return nil // ignore invalid data
	}
	db.SetDate(year, month, day)
	pos.SetTime(db.Date())

	if first {
		pos.Set(model.KeyPdop, parser.NextDouble())
		pos.Set(model.KeyHdop, parser.NextDouble())
		pos.Set(model.KeyVdop, parser.NextDouble())
	} else {
		pos.Set(model.KeyHdop, parser.NextDouble())
	}

	io := mustBin(parser)
	pos.Set(model.KeyStatus, io)
	check := func(i int) bool { return kit.Check(int64(io), i) }
	if first {
		pos.AddAlarm(alarmIf(check(0), model.AlarmSos))
		pos.Set(model.PrefixIn+"3", check(4))
		pos.Set(model.PrefixIn+"4", check(5))
		pos.Set(model.PrefixIn+"1", check(6))
		pos.Set(model.PrefixIn+"2", check(7))
		pos.Set(model.PrefixOut+"1", check(8))
		pos.Set(model.PrefixOut+"2", check(9))
		pos.Set(model.KeyBattery, parser.NextDoubleDefault(0)/100.0)
	} else {
		pos.Set(model.KeyAntenna, check(0))
		pos.Set(model.KeyCharge, check(1))
		for i := 1; i <= 6; i++ {
			pos.Set(model.PrefixIn+kit.Format("%d", i), check(1+i))
		}
		for i := 1; i <= 4; i++ {
			pos.Set(model.PrefixOut+kit.Format("%d", i), check(7+i))
		}
		pos.Set(model.KeyBattery, parser.NextDoubleDefault(0)/10.0)
	}

	pos.Set(model.KeyPower, parser.NextDoubleDefault(0))
	pos.Set(model.PrefixAdc+"1", parser.NextNullable())

	lac := parser.NextHexIntDefault(0)
	cid := parser.NextHexIntDefault(0)
	if lac != 0 && cid != 0 {
		pos.Network = model.NetworkOf(m.CellTowerFromLacCid(lac, int64(cid)))
	}

	pos.Set(model.PrefixTemp+"1", parser.NextNullable())
	pos.Set(model.KeyOdometer, parser.NextDoubleDefault(0)*1000)

	return pos
}

func decode3(m *ingest.Message, sentence string) *model.Position {
	parser := kit.NewParser(pattern3, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNext() {
		pos.AddAlarm(decodeAlarm123(parseShortHex(parser.Next())))
	}

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Set(model.PrefixIo+"1", parser.NextNullable())
	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0)/10.0)
	pos.Set(model.KeyPower, parser.NextDoubleDefault(0))
	pos.Set(model.PrefixAdc+"1", parser.NextNullable())
	pos.Set(model.PrefixAdc+"2", parser.NextNullable())
	pos.Set(model.PrefixTemp+"1", parser.NextNullable())
	pos.Set(model.PrefixTemp+"2", parser.NextNullable())

	lac := parser.NextHexIntDefault(0)
	cid := parser.NextHexIntDefault(0)
	pos.Network = model.NetworkOf(m.CellTowerFromLacCid(lac, int64(cid)))

	pos.Valid = parser.Next() == "A"
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Course = parser.NextDoubleDefault(0)
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Set(model.KeyPdop, parser.NextDouble())
	pos.Set(model.KeyOdometer, parser.NextIntDefault(0)*1000)

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	return pos
}

func decode4(m *ingest.Message, sentence string) *model.Position {
	typ := kit.IntRadix(kit.Substring(sentence, 6, 8), 16)

	switch typ {
	case 0xE2:
		return decodeE2(m, sentence)
	case 0xE5:
		return decodeE5(m, sentence)
	}

	parser := kit.NewParser(pattern4, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.AddAlarm(decodeAlarm4(typ))

	status := parser.MustHexLong()
	check := func(i int) bool { return kit.Check(status, i) }

	pos.AddAlarm(alarmIf(check(32-1), model.AlarmSos))
	pos.Set(model.KeyIgnition, check(32-2))
	pos.AddAlarm(alarmIf(check(32-3), model.AlarmOverspeed))
	pos.Set(model.KeyCharge, check(32-4))
	pos.AddAlarm(alarmIf(check(32-5), model.AlarmGeofenceExit))
	pos.AddAlarm(alarmIf(check(32-6), model.AlarmGeofenceEnter))
	pos.AddAlarm(alarmIf(check(32-7), model.AlarmGpsAntennaCut))
	pos.Set(model.PrefixOut+"1", check(32-9))
	pos.Set(model.PrefixOut+"2", check(32-10))
	pos.Set(model.PrefixOut+"3", check(32-11))
	pos.Set(model.KeyStatus, status) // see https://github.com/traccar/traccar/pull/4762

	pos.SetTime(parser.NextDateTimeDefault())

	if parser.HasNextN(2) {
		pos.Set(model.KeyBattery, parser.MustDouble()/10.0)
		pos.Set(model.KeyPower, parser.MustDouble())
	}
	if parser.HasNext() {
		pos.Set(model.KeyBattery, parser.MustDouble()/100.0)
	}

	pos.Set(model.PrefixAdc+"1", parser.NextNullable())
	pos.Set(model.PrefixAdc+"2", parser.NextNullable())
	pos.Set(model.PrefixAdc+"3", parser.NextNullable())
	pos.Set(model.PrefixAdc+"4", parser.NextNullable())
	pos.Set(model.PrefixTemp+"1", parser.NextNullable())

	if parser.HasNext() {
		pos.Set(model.PrefixTemp+"2", parser.NextNullable())
		pos.Valid = check(32 - 20)
	} else {
		pos.Valid = check(32 - 18)
	}

	lac := parser.MustHexInt()
	cid := parser.MustHexInt()
	var cellTower model.CellTower
	if parser.HasNextN(2) {
		mnc := parser.MustInt()
		mcc := parser.MustInt()
		cellTower = model.NewCellTower(mcc, mnc, lac, int64(cid))
	} else {
		cellTower = m.CellTowerFromLacCid(lac, int64(cid))
	}
	pos.Set(model.KeySatellites, parser.NextInt())
	cellTower.SetSignalStrength(parser.MustInt())
	pos.Network = model.NetworkOf(cellTower)

	pos.Course = parser.MustDouble()
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Set(model.KeyOdometer, parser.MustInt()*1000)

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	return pos
}

func decodeE2(m *ingest.Message, sentence string) *model.Position {
	parser := kit.NewParser(patternE2, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLongitude(parser.MustDouble())
	pos.SetLatitude(parser.MustDouble())

	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())

	return pos
}

func decodeE5(m *ingest.Message, sentence string) *model.Position {
	parser := kit.NewParser(patternE5, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	pos.Set(model.KeyOdometer, parser.NextLong())
	pos.Set(model.KeyFuelUsed, parser.NextInt())
	pos.Set(model.KeyFuelConsumption, parser.NextInt())
	pos.Set(model.KeyPower, float64(parser.MustInt())/1000.0)
	pos.Set(model.KeyRpm, parser.NextInt())
	pos.Set(model.KeyObdSpeed, parser.NextInt())
	parser.NextInt() // intake flow
	parser.NextInt() // intake pressure
	pos.Set(model.KeyCoolantTemp, parser.NextInt())
	pos.Set("intakeTemp", parser.NextInt())
	pos.Set(model.KeyEngineLoad, parser.NextInt())
	pos.Set(model.KeyThrottle, parser.NextInt())
	pos.Set(model.KeyFuel, parser.NextInt())

	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	var pos *model.Position
	switch {
	case kit.CharAt(sentence, 2) == '0':
		pos = decode4(m, sentence)
	case strings.Contains(sentence, "$GPRMC"):
		pos = decode12(m, sentence, true)
	default:
		index := kit.IndexOf(sentence, "|")
		if index != -1 && kit.IndexOfFrom(sentence, "|", index+1) != -1 {
			pos = decode12(m, sentence, false)
		} else {
			pos = decode3(m, sentence)
		}
	}

	if m.HasChannel() {
		if kit.CharAt(sentence, 2) == '0' {
			response := "$$0014AA" + kit.Substring(sentence, kit.Len(sentence)-6, kit.Len(sentence)-2)
			response += strings.ToUpper(kit.Format("%02X", kit.XORString(response)))
			m.Send(response)
		} else {
			m.Send("ACK OK\r\n")
		}
	}

	if pos == nil {
		return nil
	}
	return pos
}

// javaFormatObject is String.format("%s") of an attribute value: "null" when absent.
func javaFormatObject(c *model.Command, key string) string {
	if v, ok := c.Attributes.Get(key); ok && v != nil {
		return model.JavaToString(v)
	}
	return "null"
}

// formatContent is TotemProtocolEncoder.formatContent; false is Java's null.
func formatContent(c *model.Command) (string, bool) {
	password := javaFormatObject(c, model.CommandKeyDevicePassword)
	switch c.Type {
	case model.CommandCustom:
		return password + "," + javaFormatObject(c, model.CommandKeyData), true
	case model.CommandRebootDevice:
		return password + ",006", true
	case model.CommandFactoryReset:
		return password + ",007", true
	case model.CommandGetVersion:
		return password + ",056", true
	case model.CommandPositionSingle:
		return password + ",012", true
	// Assuming PIN 8 (Output C) is the power wire, like manual says, but it can be PIN 5,7,8
	case model.CommandEngineStop:
		return password + ",025,C,1", true
	case model.CommandEngineResume:
		return password + ",025,C,0", true
	}
	return "", false
}

func initDevicePassword(m *ingest.Message, c *model.Command) {
	if !c.Attributes.Has(model.CommandKeyDevicePassword) {
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), "000000"))
	}
}

// encode is TotemProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	initDevicePassword(m, c)

	content, ok := formatContent(c)
	if !ok {
		kit.Throw("NullPointerException: unsupported command %s", c.Type)
	}
	built := kit.Format("$$%04dCF%s", 10+len(kit.ASCIIBytes(content)), content)
	return kit.Format("%s%02X", built, kit.XORString(built))
}

// SMSEncoder is TotemProtocolSmsEncoder, the protocol's text (SMS) command encoder.
var SMSEncoder = ingest.CommandEncoderFunc(func(m *ingest.Message, c *model.Command) any {
	initDevicePassword(m, c)

	content, ok := formatContent(c)
	if !ok {
		content = "null"
	}
	return "*" + content + "#"
})
