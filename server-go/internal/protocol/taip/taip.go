// Package taip is the TAIP protocol (TaipProtocol): Trimble ASCII Interface Protocol
// sentences (">RPV...;ID=...<") over TCP and UDP.
package taip

import (
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: TCP and UDP servers; taip.prefix adds a 4-byte
// prefix to everything sent.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	var frameEncoder func([]byte) []byte
	if cfg.Bool("taip.prefix", false) {
		frameEncoder = PrefixEncode
	}
	pipeline := func(framed bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			p := ingest.Pipeline{
				Text:         true,
				TextOut:      true,
				FrameEncoder: frameEncoder,
				Decoder:      ingest.DecoderFunc(decode),
			}
			if framed {
				p.Framer = ingest.NewDelimiterFramer(ingest.MaxFrameLength, "<")
			}
			return p
		}
	}
	return ingest.Protocol{
		Name: "taip",
		Servers: []ingest.Server{
			{NewPipeline: pipeline(true)},
			{Datagram: true, NewPipeline: pipeline(false)},
		},
	}, nil
}

// PrefixEncode is TaipPrefixEncoder: 0x20 0x20 0x06 0x00 before each message.
func PrefixEncode(msg []byte) []byte {
	return append([]byte{0x20, 0x20, 0x06, 0x00}, msg...)
}

var pattern = kit.NewPattern().
	GroupBegin().
	Expression("R[EP]V"). // type
	GroupBegin().
	Number("(dd)").   // event
	Number("(dddd)"). // week
	Number("(d)").    // day
	GroupEndWith("?").
	Number("(d{5})"). // seconds
	Or().
	Expression("(?:RGP|RCQ|RCV|RBR|RUS00|RPI),?"). // type
	Number("(dd)?").                               // event
	Number("(dd)(dd)(dd)").                        // date (mmddyy)
	Number("(dd)(dd)(dd)").                        // time (hhmmss)
	GroupEnd().
	GroupBegin().
	Number("([-+]dd)(d{5})").  // latitude
	Number("([-+]ddd)(d{5})"). // longitude
	Or().
	Number("([-+])(dd)(dd.dddd)").  // latitude
	Number("([-+])(ddd)(dd.dddd)"). // longitude
	GroupEnd().
	Number("(ddd)"). // speed
	Number("(ddd)"). // course
	GroupBegin().
	Number("([023])"). // fix mode
	Number("xx").      // data age
	Number("(xx)").    // input
	GroupBegin().
	Number(",d+").           // flow meter
	Number(",(d+)").         // odometer
	Number(",(d{4})(d{4})"). // power / battery
	Number(",(d+)").         // rpm
	GroupBegin().
	Number(",([-+]?d+.?d*)"). // temperature 1
	Number(",([-+]?d+.?d*)"). // temperature 2
	GroupEndWith("?").
	Number(",(xx)"). // alarm
	Or().
	Number("(dd)"). // event
	Number("(dd)"). // hdop
	GroupEnd().
	Or().
	GroupBegin().
	Number("(xx)").   // input
	Number("xx").     // satellites / outputs
	Number("(ddd)").  // battery
	Number("(x{8})"). // odometer
	Number("[01]").   // gps power
	GroupBegin().
	Number("([023])"). // fix mode
	Number("(dd)").    // pdop
	Number("dd").      // satellites
	Number("xxxx").    // data age
	Number("[01]").    // modem power
	Number("[0-5]").   // gsm status
	Number("(dd)").    // rssi
	GroupBegin().
	Number("([-+]dddd)"). // temperature 1
	Number("xx").         // seconds from last
	Number("([-+]dddd)"). // temperature 2
	Number("xx").         // seconds from last
	GroupEndWith("?").
	GroupEndWith("?").
	GroupEndWith("?").
	GroupEnd().
	Any().
	Compile()

func weekTime(week, day, seconds int64) time.Time {
	return kit.NewDateBuilder(nil).
		SetDate(1980, 1, 6).
		AddMillis(((week*7+day)*24*60*60 + seconds) * 1000).
		Date()
}

func dayTime(m *ingest.Message, seconds int64) time.Time {
	date := kit.NewDateBuilderAt(m.Now, nil).
		SetTimeMillis(0, 0, 0, 0).
		AddMillis(seconds * 1000)
	return kit.CorrectDay(m.Now, date.Date(), m.Zone())
}

func decodeAlarm(value int) string {
	switch value {
	case 0x01:
		return model.AlarmSos
	case 0x02:
		return model.AlarmPowerCut
	}
	return ""
}

func decodeAlarm2(value int) string {
	switch value {
	case 22:
		return model.AlarmAcceleration
	case 23:
		return model.AlarmBraking
	case 24:
		return model.AlarmAccident
	case 26, 28:
		return model.AlarmCornering
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if beginIndex := kit.IndexOf(sentence, ">"); beginIndex != -1 {
		sentence = kit.SubstringFrom(sentence, beginIndex+1)
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	var valid *bool
	var event *int

	if parser.HasNextN(3) {
		event = parser.NextInt()
		pos.SetTime(weekTime(int64(parser.NextIntDefault(0)), int64(parser.NextIntDefault(0)),
			int64(parser.NextIntDefault(0))))
	} else if parser.HasNext() {
		pos.SetTime(dayTime(m, int64(parser.NextIntDefault(0))))
	}

	if parser.HasNext() {
		event = parser.NextInt()
	}

	if parser.HasNextN(6) {
		pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	}

	if parser.HasNextN(4) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegDeg))
		pos.SetLongitude(parser.NextCoordinate(kit.DegDeg))
	}
	if parser.HasNextN(6) {
		pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	}

	pos.Speed = m.ConvertSpeed(parser.NextDoubleDefault(0), "mph")
	pos.Course = parser.NextDoubleDefault(0)

	setValid := func(v bool) { valid = &v }

	if parser.HasNextN(2) {
		setValid(parser.MustInt() > 0)
		input := parser.MustHexInt()
		pos.Set(model.KeyIgnition, kit.Check(int64(input), 7))
		pos.Set(model.KeyInput, input)
	}

	if parser.HasNextN(7) {
		pos.Set(model.KeyOdometer, parser.NextInt())
		pos.Set(model.KeyPower, float64(parser.MustInt())/100.0)
		pos.Set(model.KeyBattery, float64(parser.MustInt())/100.0)
		pos.Set(model.KeyRpm, parser.NextInt())
		pos.Set(model.PrefixTemp+"1", parser.NextDouble())
		pos.Set(model.PrefixTemp+"2", parser.NextDouble())
		event = parser.NextHexInt()
	}

	if parser.HasNextN(2) {
		event = parser.NextInt()
		pos.Set(model.KeyHdop, parser.NextInt())
	}

	if parser.HasNextN(3) {
		pos.Set(model.KeyInput, parser.NextHexIntDefault(0))
		pos.Set(model.KeyBattery, parser.NextIntDefault(0))
		pos.Set(model.KeyOdometer, parser.NextLongRadix(16, 0))
	}

	if parser.HasNextN(3) {
		setValid(parser.MustInt() > 0)
		pos.Set(model.KeyPdop, parser.NextInt())
		pos.Set(model.KeyRssi, parser.NextInt())
	}

	if parser.HasNextN(2) {
		pos.Set(model.PrefixTemp+"1", float64(parser.MustInt())/100.0)
		pos.Set(model.PrefixTemp+"2", float64(parser.MustInt())/100.0)
	}

	pos.Valid = valid == nil || *valid

	if event != nil {
		pos.Set(model.KeyEvent, *event)
		if kit.CharAt(sentence, 5) == ',' {
			pos.AddAlarm(decodeAlarm2(*event))
		} else {
			pos.AddAlarm(decodeAlarm(*event))
		}
	}

	var attributes []string
	if beginIndex := kit.IndexOf(sentence, ";"); beginIndex != -1 {
		endIndex := kit.IndexOfFrom(sentence, "<", beginIndex)
		if endIndex == -1 {
			endIndex = kit.Len(sentence)
		}
		attributes = kit.Split(kit.Substring(sentence, beginIndex, endIndex), ";")
	}

	return decodeAttributes(m, pos, attributes)
}

func decodeAttributes(m *ingest.Message, pos *model.Position, attributes []string) any {
	var uniqueID string
	var ds *ingest.DeviceSession
	var messageIndex *string
	indexFirst := true

	for _, attribute := range attributes {
		index := kit.IndexOf(attribute, "=")
		if index != -1 {
			key := strings.ToLower(kit.Substring(attribute, 0, index))
			value := kit.SubstringFrom(attribute, index+1)
			switch key {
			case "id":
				uniqueID = value
				ds = m.Session(value)
				if ds != nil {
					pos.DeviceID = ds.DeviceID
				}
				if messageIndex == nil {
					indexFirst = false
				}
			case "io":
				pos.Set(model.KeyIgnition, kit.Check(int64(kit.CharAt(value, 0)-'0'), 0))
				pos.Set(model.KeyCharge, kit.Check(int64(kit.CharAt(value, 0)-'0'), 1))
				pos.Set(model.KeyOutput, int(kit.CharAt(value, 1)-'0'))
				pos.Set(model.KeyInput, int(kit.CharAt(value, 2)-'0'))
			case "ix":
				pos.Set(model.PrefixIo+"1", value)
			case "ad":
				pos.Set(model.PrefixAdc+"1", kit.Int(value))
			case "sv":
				pos.Set(model.KeySatellites, kit.Int(value))
			case "bl":
				pos.Set(model.KeyBattery, float64(kit.Int(value))/1000.0)
			case "vo":
				pos.Set(model.KeyOdometer, kit.Long(value))
			default:
				pos.Set(key, value)
			}
		} else if strings.HasPrefix(attribute, "#") {
			a := attribute
			messageIndex = &a
		}
	}

	if ds == nil {
		return nil
	}

	if messageIndex != nil {
		var response string
		if strings.HasPrefix(*messageIndex, "#IP") {
			response = ">SAK;ID=" + uniqueID + ";" + *messageIndex + "<"
		} else {
			if indexFirst {
				response = ">ACK;" + *messageIndex + ";ID=" + uniqueID + ";"
			} else {
				response = ">ACK;ID=" + uniqueID + ";" + *messageIndex + ";"
			}
			deviceModel := m.DeviceModel(ds)
			lantrix := strings.HasPrefix(strings.ToUpper(deviceModel), "LANTRIX")
			var checksum int
			if lantrix {
				checksum = kit.XORString(response)
			} else {
				checksum = kit.XORString(response + "*")
			}
			response += kit.Format("*%02X", checksum) + "<"
		}
		m.Send(response)
	} else {
		m.Send(uniqueID)
	}
	return pos
}
