// Package laipac is the Laipac protocol (LaipacProtocol): NMEA-like text lines ($AVRMC,
// $EAVSYS, $ECHK) over TCP.
package laipac

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// DefaultDevicePassword is the password of commands and event responses when none is set.
const DefaultDevicePassword = "00000000"

// Protocol is the protocol's definition: one TCP server of lines.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "laipac",
		DataCommands: protocol.DataCommands("laipac"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var patternEavsys = kit.NewPattern().
	Text("$EAVSYS,").
	Expression("([^,]+),").      // identifier
	Expression("([0-9]+),").     // iccid
	Expression(`(\+?[0-9]+)?,`). // sim phone number
	Expression("(?:[^,]*),").    // owner name
	Expression("([^,]*)?").      // firmware version
	Text("*").
	Number("(xx)"). // checksum
	Compile()

var patternAvrmc = kit.NewPattern().
	Text("$AVRMC,").
	Expression("([^,]+),").       // identifier
	Number("(dd)(dd)(dd),").      // time (hhmmss)
	Expression("([AVRPavrp]),").  // validity
	Number("(dd)(dd.d+),").       // latitude
	Expression("([NS]),").        // latitude hemisphere
	Number("(ddd)(dd.d+),").      // longitude
	Number("([EW]),").            // longitude hemisphere
	Number("(d+.d+),").           // speed
	Number("(d+.d+),").           // course
	Number("(dd)(dd)(dd),").      // date (ddmmyy)
	Expression("([0-9A-Za-z]),"). // event code
	Expression(`([\d.]+),`).      // battery voltage
	Number("(d+),").              // current mileage
	Number("(d),").               // gps status
	Number("(d+),").              // adc1
	Number("(d+)").               // adc2
	Number(",(xxxx|x)").          // lac | lac+cid = 0
	Number("(xxxx),").            // cid | nothing
	Number("(ddd|d)").            // mcc | mcc+mnc = 0
	Number("(ddd)").              // mnc | nothing
	OptionalN(4).
	Expression(",([^*]*)"). // anything remaining (be forward compatible)
	OptionalN(1).
	Text("*").
	Number("(xx)"). // checksum
	Compile()

func decodeAlarm(event string) string {
	switch event {
	case "Z":
		return model.AlarmLowBattery
	case "Y":
		return model.AlarmTow
	case "X":
		return model.AlarmGeofenceEnter
	case "T":
		return model.AlarmTampering
	case "H":
		return model.AlarmPowerOff
	case "8":
		return model.AlarmVibration
	case "7", "4":
		return model.AlarmGeofenceExit
	case "6":
		return model.AlarmOverspeed
	case "5":
		return model.AlarmPowerCut
	case "3":
		return model.AlarmSos
	}
	return ""
}

// decodeEvent sets the inputs an input-status event carries and returns the event, or ""
// (Java's null) for an input status.
func decodeEvent(event string, pos *model.Position, deviceModel string) string {
	if kit.Len(event) == 1 {
		c := kit.CharAt(event, 0)
		var base rune
		switch {
		case c >= 'A' && c <= 'D':
			base = 'A'
		case c >= 'O' && c <= 'R':
			base = 'O'
		default:
			return event
		}
		status := int64(c - base)
		pos.Set(model.PrefixIn+"1", kit.Check(status, 0))
		pos.Set(model.PrefixIn+"2", kit.Check(status, 1))
		if deviceModel == "SF-Lite" {
			pos.Set(model.PrefixIn+"3", base == 'O')
		}
		return ""
	}
	return event
}

func sendEventResponse(m *ingest.Message, event, devicePassword string) {
	var code string
	switch event {
	case "3":
		code = "d"
	case "M":
		code = "m"
	case "S", "T":
		code = "t"
	case "X", "4":
		code = "x"
	case "Y":
		code = "y"
	case "Z":
		code = "z"
	default:
		return
	}
	response := "$AVCFG," + devicePassword + "," + code
	m.Send(response + kit.NMEA(kit.SubstringFrom(response, 1)) + "\r\n")
}

func sendAcknowledge(m *ingest.Message, status, event, checksum string) {
	if c := kit.CharAt(status, 0); c >= 'a' && c <= 'z' { // isLowerCase, of [AVRPavrp]
		response := "$EAVACK," + event + "," + checksum
		m.Send(response + kit.NMEA(kit.SubstringFrom(response, 1)) + "\r\n")
	}
}

func decodeEavsys(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternEavsys, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	pos.Set(model.KeyIccid, parser.NextNullable())
	pos.Set(model.KeyPhone, parser.NextNullable())
	pos.Set(model.KeyVersionFw, parser.NextNullable())
	return pos
}

func decodeAvrmc(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternAvrmc, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	deviceModel := m.DeviceModel(ds)

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	date := kit.NewDateBuilder(nil).SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	status := parser.Next()
	upper := strings.ToUpper(status)
	pos.Valid = upper == "A" || upper == "R" || upper == "P"
	pos.Set(model.KeyStatus, status)

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())

	event := parser.Next()
	pos.AddAlarm(decodeAlarm(event))
	pos.Set(model.KeyEvent, decodeEvent(event, pos, deviceModel))
	pos.Set(model.KeyBattery, kit.Double(strings.ReplaceAll(parser.Next(), ".", ""))/1000.0)
	pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	pos.Set(model.KeyGps, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.MustDouble()/1000.0)

	if deviceModel == "AVL110" || deviceModel == "AVL120" {
		pos.Set(model.PrefixAdc+"2", parser.MustDouble()/1000.0)
	} else {
		parser.Next()
	}

	lac, cid := parser.NextHexInt(), parser.NextHexInt()
	mcc, mnc := parser.NextInt(), parser.NextInt()
	if lac != nil && cid != nil && mcc != nil && mnc != nil {
		pos.Network = model.NetworkOf(model.NewCellTower(*mcc, *mnc, *lac, int64(*cid)))
	}

	parser.Next() // unused
	checksum := parser.Next()

	if m.HasChannel() {
		sendAcknowledge(m, status, event, checksum)
		sendEventResponse(m, event, m.DevicePassword(ds.DeviceID, m.Protocol(), DefaultDevicePassword))
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	switch {
	case strings.HasPrefix(sentence, "$ECHK"):
		m.Send(sentence + "\r\n")
	case strings.HasPrefix(sentence, "$EAVSYS"):
		return decodeEavsys(m, sentence)
	case strings.HasPrefix(sentence, "$AVRMC"):
		return decodeAvrmc(m, sentence)
	}
	return nil
}

// initDevicePassword is BaseProtocolEncoder.initDevicePassword.
func initDevicePassword(m *ingest.Message, c *model.Command, def string) {
	if !c.Attributes.Has(model.CommandKeyDevicePassword) {
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), def))
	}
}

func encode(m *ingest.Message, c *model.Command) any {
	initDevicePassword(m, c, DefaultDevicePassword)
	format := func(format string, keys ...string) string {
		sentence := kit.FormatCommand(c, m.UniqueID(c.DeviceID), "$"+format, keys...)
		return sentence + kit.NMEA(kit.SubstringFrom(sentence, 1)) + "\r\n"
	}
	switch c.Type {
	case model.CommandCustom:
		return format("%s", model.CommandKeyData)
	case model.CommandPositionSingle:
		return format("AVREQ,%s,1", model.CommandKeyDevicePassword)
	case model.CommandRebootDevice:
		return format("AVRESET,%s,%s", model.CommandKeyUniqueID, model.CommandKeyDevicePassword)
	}
	return nil
}
