// Package minifinder is the MiniFinder protocol (MiniFinderProtocol): "!X," text
// messages ended by ';', and text commands.
package minifinder

import (
	"regexp"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "minifinder",
		DataCommands: protocol.DataCommands("minifinder"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ";\x00", ";"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var patternFix = kit.NewPattern().
	Number("(d+)/(d+)/(d+),"). // date (dd/mm/yy)
	Number("(d+):(d+):(d+),"). // time (hh:mm:ss)
	Number("(-?d+.d+),").      // latitude
	Number("(-?d+.d+),")       // longitude

var patternState = kit.NewPattern().
	Number("(d+.?d*),").  // speed (km/h)
	Number("(d+.?d*),").  // course
	Number("(x+),").      // flags
	Number("(-?d+.d+),"). // altitude (meters)
	Number("(d+),")       // battery (percentage)

var patternA = kit.NewPattern().
	Text("!A,").
	Expression(patternFix.String()).
	Any(). // unknown 3 fields
	Compile()

var patternC = kit.NewPattern().
	Text("!C,").
	Expression(patternFix.String()).
	Expression(patternState.String()).
	Any(). // unknown 3 fields
	Compile()

var patternBD = kit.NewPattern().
	Expression("![BD],"). // B - buffered, D - live
	Expression(patternFix.String()).
	Expression(patternState.String()).
	Number("(d+),").    // satellites in use
	Number("(d+),").    // satellites in view
	Number("(d+.?d*)"). // hdop
	Compile()

func decodeFix(m *ingest.Message, pos *model.Position, parser *kit.Parser) {
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
}

func decodeFlags(pos *model.Position, flags int) {
	f := int64(flags)
	pos.Valid = kit.ToInt(flags, 2) > 0
	if kit.Check(f, 1) {
		pos.Set(model.KeyApproximate, true)
	}
	if kit.Check(f, 2) {
		pos.AddAlarm(model.AlarmFault)
	}
	if kit.Check(f, 6) {
		pos.AddAlarm(model.AlarmSos)
	}
	if kit.Check(f, 7) {
		pos.AddAlarm(model.AlarmOverspeed)
	}
	if kit.Check(f, 8) {
		pos.AddAlarm(model.AlarmFallDown)
	}
	if kit.Check(f, 9) || kit.Check(f, 10) || kit.Check(f, 11) {
		pos.AddAlarm(model.AlarmGeofence)
	}
	if kit.Check(f, 12) {
		pos.AddAlarm(model.AlarmLowBattery)
	}
	if kit.Check(f, 15) || kit.Check(f, 14) {
		pos.AddAlarm(model.AlarmMovement)
	}
	pos.Set(model.KeyRssi, kit.BetweenInt(flags, 16, 21))
	pos.Set(model.KeyCharge, kit.Check(f, 22))
}

func decodeState(pos *model.Position, parser *kit.Parser) {
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	if pos.Course > 360 {
		pos.Course = 0
	}
	decodeFlags(pos, parser.NextHexIntDefault(0))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Set(model.KeyBatteryLevel, parser.NextIntDefault(0))
}

// typePattern is "![345A-D],.*" as String.matches: '.' excludes Java's line terminators.
var typePattern = regexp.MustCompile("^![345A-D],[^\n\r\u0085  ]*$")

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if len(sentence) >= 3 && sentence[:3] == "!1," {
		index := kit.IndexOfFrom(sentence, ",", 3)
		if index < 0 {
			index = kit.Len(sentence)
		}
		m.Session(kit.Substring(sentence, 3, index))
		return nil
	}

	ds := m.Session()
	if ds == nil || !typePattern.MatchString(sentence) {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	typ := kit.Substring(sentence, 1, 2)
	pos.Set(model.KeyType, typ)

	switch typ {
	case "3":
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, kit.SubstringFrom(sentence, 3))
		return pos
	case "4":
		values := kit.Split(sentence, ",")
		m.LastLocation(pos, nil)
		for i := 1; i <= 3; i++ {
			if values[i+1] != "" {
				pos.Set("phone"+strconv.Itoa(i), values[i+1])
			}
		}
		return pos
	case "5":
		values := kit.Split(sentence, ",")
		m.LastLocation(pos, nil)
		pos.Set(model.KeyRssi, kit.Int(values[1]))
		if len(values) >= 4 {
			pos.Set(model.KeyBatteryLevel, kit.Int(values[3]))
		}
		return pos
	case "B", "D":
		parser := kit.NewParser(patternBD, sentence)
		if !parser.Matches() {
			return nil
		}
		decodeFix(m, pos, parser)
		decodeState(pos, parser)
		pos.Set(model.KeySatellites, parser.NextIntDefault(0))
		pos.Set(model.KeySatellitesVisible, parser.NextIntDefault(0))
		pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))
		return pos
	case "C":
		parser := kit.NewParser(patternC, sentence)
		if !parser.Matches() {
			return nil
		}
		decodeFix(m, pos, parser)
		decodeState(pos, parser)
		return pos
	case "A":
		parser := kit.NewParser(patternA, sentence)
		if !parser.Matches() {
			return nil
		}
		decodeFix(m, pos, parser)
		return pos
	}
	return nil
}

// rawOffset is TimeZone.getRawOffset: the zone's standard offset (without daylight
// saving), in milliseconds, as of now.
func rawOffset(loc *time.Location, now time.Time) int {
	t := now.In(loc)
	_, offset := t.Zone()
	if t.IsDST() {
		for month := time.January; month <= time.December; month++ {
			if u := time.Date(t.Year(), month, 1, 12, 0, 0, 0, loc); !u.IsDST() {
				_, offset = u.Zone()
				break
			}
		}
	}
	return offset * 1000
}

// formatValue is MiniFinderProtocolEncoder.formatValue; false is Java's null.
func formatValue(now time.Time) func(key string, value any) (string, bool) {
	return func(key string, value any) (string, bool) {
		switch key {
		case model.CommandKeyEnable:
			b, ok := value.(bool)
			if !ok {
				kit.Throw("ClassCastException or NullPointerException: %s", key)
			}
			if b {
				return "1", true
			}
			return "0", true
		case model.CommandKeyTimezone:
			s, ok := value.(string)
			if !ok {
				kit.Throw("ClassCastException or NullPointerException: %s", key)
			}
			return kit.Format("%+03d", rawOffset(javatime.TimeZone(s), now)/3600000), true
		case model.CommandKeyIndex:
			var n int
			switch v := value.(type) {
			case int64:
				n = int(int32(v))
			case float64:
				n = int(int32(v))
			default:
				kit.Throw("ClassCastException or NullPointerException: %s", key)
			}
			switch n {
			case 0:
				return "A", true
			case 1:
				return "B", true
			case 2:
				return "C", true
			}
		}
		return "", false
	}
}

func encode(m *ingest.Message, c *model.Command) any {
	if !c.Attributes.Has(model.CommandKeyDevicePassword) {
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), "123456"))
	}
	formatter := formatValue(m.Now)
	plain := func(format string, keys ...string) string {
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), format, keys...)
	}
	with := func(format string, keys ...string) string {
		return kit.FormatCommandWith(c, m.UniqueID(c.DeviceID), format, formatter, keys...)
	}
	pw := model.CommandKeyDevicePassword
	switch c.Type {
	case model.CommandSetTimezone:
		return with("%sL%s", pw, model.CommandKeyTimezone)
	case model.CommandVoiceMonitoring:
		return with("%sP%s", pw, model.CommandKeyEnable)
	case model.CommandAlarmSpeed:
		return plain("%sJ1%s", pw, model.CommandKeyData)
	case model.CommandAlarmGeofence:
		return plain("%sR1%s", pw, model.CommandKeyRadius)
	case model.CommandAlarmVibration:
		return plain("%sW1,%s", pw, model.CommandKeyData)
	case model.CommandSetAgps:
		return with("%sAGPS%s", pw, model.CommandKeyEnable)
	case model.CommandAlarmFall:
		return with("%sF%s", pw, model.CommandKeyEnable)
	case model.CommandModePowerSaving:
		return with("%sSP%s", pw, model.CommandKeyEnable)
	case model.CommandModeDeepSleep:
		return with("%sDS%s", pw, model.CommandKeyEnable)
	case model.CommandSosNumber:
		return with("%s%s1,%s", pw, model.CommandKeyIndex, model.CommandKeyPhone)
	case model.CommandSetIndicator:
		return plain("%sLED%s", pw, model.CommandKeyData)
	}
	return nil
}
