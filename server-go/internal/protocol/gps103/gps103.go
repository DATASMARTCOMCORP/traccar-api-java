// Package gps103 is the GPS103 protocol (Gps103Protocol): "imei:..." text reports (TK103
// and compatible trackers) over TCP and UDP, with OBD, alternative and photo messages.
package gps103

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "gps103",
		DataCommands: protocol.DataCommands("gps103"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				framer := ingest.NewDelimiterFramer(ingest.MaxFrameLengthLarge, "\r\n", "\n", ";", "*")
				framer.KeepEnding = true
				return ingest.Pipeline{
					Framer:  framer,
					Text:    true,
					TextOut: true,
					Decoder: &Decoder{},
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Text:    true,
					TextOut: true,
					Decoder: &Decoder{},
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}
}

// numberLongValue is ((Number) value).longValue().
func numberLongValue(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		switch {
		case x != x:
			return 0
		case x >= math.MaxInt64:
			return math.MaxInt64
		case x <= math.MinInt64:
			return math.MinInt64
		}
		return int64(x)
	case nil:
		kit.Throw("NullPointerException")
	}
	kit.Throw("ClassCastException: %T is not a Number", v)
	return 0
}

// formatValue is Gps103ProtocolEncoder.formatValue: the frequency in hours, minutes or
// seconds.
func formatValue(key string, value any) (string, bool) {
	if key != model.CommandKeyFrequency {
		return "", false
	}
	frequency := numberLongValue(value)
	switch {
	case frequency/60/60 > 0:
		return kit.Format("%02dh", frequency/60/60), true
	case frequency/60 > 0:
		return kit.Format("%02dm", frequency/60), true
	}
	return kit.Format("%02ds", frequency), true
}

func encode(m *ingest.Message, c *model.Command) any {
	uniqueID := m.UniqueID(c.DeviceID)
	f := func(format string, keys ...string) any {
		return kit.FormatCommand(c, uniqueID, format, keys...)
	}
	switch c.Type {
	case model.CommandCustom:
		return f("**,imei:%s,%s", model.CommandKeyUniqueID, model.CommandKeyData)
	case model.CommandPositionStop:
		return f("**,imei:%s,D", model.CommandKeyUniqueID)
	case model.CommandPositionSingle:
		return f("**,imei:%s,B", model.CommandKeyUniqueID)
	case model.CommandPositionPeriodic:
		return kit.FormatCommandWith(c, uniqueID, "**,imei:%s,C,%s", formatValue,
			model.CommandKeyUniqueID, model.CommandKeyFrequency)
	case model.CommandEngineStop:
		return f("**,imei:%s,J", model.CommandKeyUniqueID)
	case model.CommandEngineResume:
		return f("**,imei:%s,K", model.CommandKeyUniqueID)
	case model.CommandAlarmArm:
		return f("**,imei:%s,L", model.CommandKeyUniqueID)
	case model.CommandAlarmDisarm:
		return f("**,imei:%s,M", model.CommandKeyUniqueID)
	case model.CommandRequestPhoto:
		return f("**,imei:%s,160", model.CommandKeyUniqueID)
	}
	return nil
}

// Decoder is Gps103ProtocolDecoder: a photo's packet count and data span messages.
type Decoder struct {
	photoPackets int
	media        *kit.Buf
}

var pattern = kit.NewPattern().
	Text("imei:").
	Number("(d+),").        // imei
	Expression("([^,]*),"). // alarm
	GroupBegin().
	Number("(dd)/?(dd)/?(dd) ?"). // local date (yymmdd)
	Number("(dd):?(dd)(?:dd)?,"). // local time (hhmmss)
	Or().
	Number("d*,").
	GroupEnd().
	Expression("([^,]+)?,"). // rfid
	GroupBegin().
	Text("L,").
	GroupBegin().
	Text(",,").
	Number("(x+),,").  // lac
	Number("(x+),,,"). // cid
	GroupEndWith("?").
	Or().
	Text("F,").
	GroupBegin().
	Number("(dd)(dd)(dd)(?:.d+)?"). // time utc (hhmmss)
	Or().
	Number("(?:d{1,5}.d+)?").
	GroupEnd().
	Text(",").
	Expression("([AV]),").             // validity
	Expression("([NS]),").Optional().  //
	Number("(d+)(dd.d+),").            // latitude (ddmm.mmmm)
	Expression("([NS]),").Optional().  //
	Expression("([EW]),").Optional().  //
	Number("(d+)(dd.d+),").            // longitude (dddmm.mmmm)
	Expression("([EW])?,").Optional(). //
	Number("(d+.?d*)?").Optional().    // speed
	Number(",(d+.?d*)?").Optional().   // course
	Number(",(-?d+.?d*)?").Optional(). // altitude
	Number(",([01])?").Optional().     // ignition
	Number(",([01])?").Optional().     // door
	GroupBegin().
	Number(",(?:(d+.d+)%)?").    // fuel 1
	Number(",(?:(d+.d+)%|d+)?"). // fuel 2
	GroupEndWith("?").
	Number(",([-+]?d+)?").Optional(). // temperature
	GroupEnd().
	Any().
	Compile()

var patternOBD = kit.NewPattern().
	Text("imei:").
	Number("(d+),").                // imei
	Expression("OBD,").             // type
	Number("(dd)(dd)(dd)").         // date (yymmdd)
	Number("(dd)(dd)(dd),").        // time (hhmmss)
	Number("(d+)?,").               // odometer
	Number("(d+.d+)?,").            // fuel instant
	Number("(d+.d+)?,").            // fuel average
	Number("(d+)?,").               // hours
	Number("(d+),").                // speed
	Number("(d+.?d*%),").           // power load
	Number("(?:([-+]?d+)|[-+]?),"). // temperature
	Number("(d+.?d*%),").           // throttle
	Number("(d+),").                // rpm
	Number("(d+.d+),").             // battery
	Number("([^;]*)").              // dtcs
	Any().
	Compile()

var patternAlt = kit.NewPattern().
	Text("imei:").
	Number("(d+),"). // imei
	Expression("[^,]+,").
	Expression("(?:-+|(.+)),"). // event
	Expression("(?:-+|(.+)),"). // sensor id
	Expression("(?:-+|(.+)),"). // sensor voltage
	Number("(dd)(dd)(dd),").    // time (hhmmss)
	Number("(dd)(dd)(dd),").    // date (ddmmyy)
	Number("(d+),").            // rssi
	Number("(d),").             // gps status
	Number("(-?d+.d+),").       // latitude
	Number("(-?d+.d+),").       // longitude
	Number("(d+),").            // speed
	Number("(d+),").            // course
	Number("(-?d+),").          // altitude
	Number("(d+.d+),").         // hdop
	Number("(d+),").            // satellites
	Number("([01]),").          // ignition
	Number("([01]),").          // charge
	Expression("(?:-+|(.+))").  // error
	Any().
	Compile()

var patternHandshake = regexp.MustCompile(`imei:(\d+),`)

func decodeAlarm(value string) string {
	if strings.HasPrefix(value, "T:") {
		return model.AlarmTemperature
	} else if strings.HasPrefix(value, "oil") {
		return model.AlarmFuelLeak
	}
	switch value {
	case "help me":
		return model.AlarmSos
	case "low battery":
		return model.AlarmLowBattery
	case "stockade":
		return model.AlarmGeofence
	case "move":
		return model.AlarmMovement
	case "speed":
		return model.AlarmOverspeed
	case "door alarm":
		return model.AlarmDoor
	case "ac alarm":
		return model.AlarmPowerCut
	case "accident alarm":
		return model.AlarmAccident
	case "sensor alarm":
		return model.AlarmVibration
	case "bonnet alarm":
		return model.AlarmBonnet
	case "footbrake alarm":
		return model.AlarmFootBrake
	case "DTC":
		return model.AlarmFault
	}
	return ""
}

func (d *Decoder) decodeRegular(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	imei := parser.Next()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	alarm := parser.Next()
	pos.AddAlarm(decodeAlarm(alarm))
	switch {
	case alarm == "help me":
		m.Send("**,imei:" + imei + ",E;")
	case strings.HasPrefix(alarm, "vt"):
		d.photoPackets = kit.Int(kit.SubstringFrom(alarm, 2))
		d.media = m.NewMediaBuffer()
	case alarm == "acc on":
		pos.Set(model.KeyIgnition, true)
	case alarm == "acc off":
		pos.Set(model.KeyIgnition, false)
	case strings.HasPrefix(alarm, "T:"):
		pos.Set(model.PrefixTemp+"1", kit.Double(kit.SubstringFrom(alarm, 2)))
	case strings.HasPrefix(alarm, "oil "):
		pos.Set(model.KeyFuel, kit.Double(kit.SubstringFrom(alarm, 4)))
	case !pos.Attributes.Has(model.KeyAlarm) && alarm != "tracker":
		pos.Set(model.KeyEvent, alarm)
	}

	date := kit.NewDateBuilder(nil).
		SetDate(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	localHours := parser.NextIntDefault(0)
	localMinutes := parser.NextIntDefault(0)

	rfid := parser.NextNullable()
	if alarm == "rfid" {
		pos.Set(model.KeyDriverUniqueID, rfid)
	}

	if parser.HasNextN(2) {
		mcc, err := m.Config().Int("geolocation.mcc", 0)
		if err != nil {
			panic(err)
		}
		mnc, err := m.Config().Int("geolocation.mnc", 0)
		if err != nil {
			panic(err)
		}
		lac := parser.NextHexIntDefault(0)
		cid := parser.NextHexIntDefault(0)
		pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	}

	if parser.HasNextAny(20) {
		utcHours := parser.NextNullable()
		utcMinutes := parser.NextNullable()

		date.SetTime(localHours, localMinutes, parser.NextIntDefault(0))

		// Timezone calculation
		if utcHours != nil && utcMinutes != nil {
			deltaMinutes := (localHours - kit.Int(*utcHours)) * 60
			deltaMinutes += localMinutes - kit.Int(*utcMinutes)
			if deltaMinutes <= -12*60 {
				deltaMinutes += 24 * 60
			} else if deltaMinutes > 12*60 {
				deltaMinutes -= 24 * 60
			}
			date.AddMinute(-deltaMinutes)
		}
		pos.SetTime(date.Date())

		pos.Valid = parser.Next() == "A"
		pos.FixTime = pos.DeviceTime
		pos.SetLatitude(parser.NextCoordinate(kit.HemDegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDegMinHem))
		pos.Speed = parser.NextDoubleDefault(0)
		pos.Course = parser.NextDoubleDefault(0)
		pos.Altitude = parser.NextDoubleDefault(0)

		if parser.HasNext() {
			pos.Set(model.KeyIgnition, parser.MustInt() == 1)
		}
		if parser.HasNext() {
			pos.Set(model.KeyDoor, parser.MustInt() == 1)
		}
		pos.Set("fuel1", parser.NextDouble())
		pos.Set("fuel2", parser.NextDouble())
		pos.Set(model.PrefixTemp+"1", parser.NextInt())
	} else {
		m.LastLocation(pos, nil)
	}

	return pos
}

func decodeOBD(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternOBD, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := parser.NextDateTimeDefault()
	m.LastLocation(pos, &t)

	pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
	parser.NextDoubleDefault(0) // instant fuel consumption
	pos.Set(model.KeyFuelConsumption, parser.NextDoubleDefault(0))
	if parser.HasNext() {
		pos.Set(model.KeyHours, units.MsFromHours(int64(parser.MustInt())))
	}
	pos.Set(model.KeyObdSpeed, parser.NextIntDefault(0))
	pos.Set(model.KeyEngineLoad, parser.NextNullable())
	pos.Set(model.KeyCoolantTemp, parser.NextInt())
	pos.Set(model.KeyThrottle, parser.NextNullable())
	pos.Set(model.KeyRpm, parser.NextIntDefault(0))
	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0))
	pos.Set(model.KeyDtcs, kit.Trim(strings.ReplaceAll(parser.Next(), ",", " ")))

	return pos
}

func decodeAlternative(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternAlt, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyEvent, parser.NextNullable())
	pos.Set("sensorId", parser.NextNullable())
	pos.Set("sensorVoltage", parser.NextDouble())

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))

	pos.Set(model.KeyRssi, parser.NextInt())

	pos.Valid = parser.MustInt() > 0
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())

	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyIgnition, parser.MustInt() > 0)
	pos.Set(model.KeyCharge, parser.MustInt() > 0)
	pos.Set("error", parser.NextNullable())

	return pos
}

func (d *Decoder) decodePhoto(m *ingest.Message, sentence string) any {
	imei := kit.Substring(sentence, 5, 5+15)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	end := kit.Len(sentence)
	if strings.HasSuffix(sentence, ";") {
		end--
	}
	buf := kit.NewBuf(kit.ParseHex(kit.Substring(sentence, 24, end)))
	index := buf.ReadUnsignedShortLE()
	buf.Skip(2)
	if d.media == nil {
		kit.Throw("NullPointerException: media buffer")
	}
	d.media.WriteBytes(buf.ReadBytes(buf.ReadableBytes() - 2))

	if index+1 >= d.photoPackets {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		data := d.media.Bytes()
		d.media = nil
		d.photoPackets = 0
		pos.Set(model.KeyImage, m.WriteMedia(imei, "jpg", data))

		return pos
	}
	return nil
}

// startsWithAt is String.startsWith(prefix, offset), with UTF-16 indexes.
func startsWithAt(s, prefix string, offset int) bool {
	if offset < 0 || offset+kit.Len(prefix) > kit.Len(s) {
		return false
	}
	return kit.Substring(s, offset, offset+kit.Len(prefix)) == prefix
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if strings.Contains(sentence, "imei:") && kit.Len(sentence) <= 30 {
		if m.HasChannel() {
			m.Send("LOAD")
			if match := patternHandshake.FindStringSubmatch(sentence); match != nil {
				m.Session(match[1])
			}
		}
		return nil
	}

	if sentence != "" && unicode.IsDigit(kit.CharAt(sentence, 0)) {
		m.Send("ON")
		start := kit.IndexOf(sentence, "imei:")
		if start < 0 {
			return nil
		}
		sentence = kit.SubstringFrom(sentence, start)
	}

	switch {
	case startsWithAt(sentence, "vr", 21):
		return d.decodePhoto(m, sentence)
	case strings.Contains(kit.Substring(sentence, 21, 21+3), "OBD"):
		return decodeOBD(m, sentence)
	case strings.HasSuffix(sentence, "*"):
		return decodeAlternative(m, sentence)
	}
	return d.decodeRegular(m, sentence)
}
