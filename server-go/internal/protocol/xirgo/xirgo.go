// Package xirgo is the Xirgo protocol (XirgoProtocol): "$$...##" text messages in a fixed
// layout (old or new, detected from the first message) or in a configured form, over TCP
// and UDP.
package xirgo

import (
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP server ("##"-delimited) and a UDP server.
// The decoder reads xirgo.form.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	var form *string
	if v, ok := cfg.Lookup("xirgo.form"); ok {
		form = &v
	}
	return ingest.Protocol{
		Name:         "xirgo",
		DataCommands: protocol.DataCommands("xirgo"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "##"),
					Text:    true,
					TextOut: true,
					Decoder: &Decoder{Form: form},
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Text:    true,
					TextOut: true,
					Decoder: &Decoder{Form: form, Datagram: true},
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}, nil
}

// Decoder is XirgoProtocolDecoder. Form is <protocol>.form (nil: the fixed layouts);
// Datagram is set on the UDP server, where messages are acknowledged.
type Decoder struct {
	Form     *string
	Datagram bool

	newFormat *bool // the layout of the first message, nil until one matches
}

var patternOld = kit.NewPattern().
	Text("$$").
	Number("(d+),").             // imei
	Number("(d+),").             // event
	Number("(dddd)/(dd)/(dd),"). // date (yyyy/mm/dd)
	Number("(dd):(dd):(dd),").   // time (hh:mm:ss)
	Number("(-?d+.?d*),").       // latitude
	Number("(-?d+.?d*),").       // longitude
	Number("(-?d+.?d*),").       // altitude
	Number("(d+.?d*),").         // speed
	Number("(d+.?d*),").         // course
	Number("(d+),").             // satellites
	Number("(d+.?d*),").         // hdop
	Number("(d+.d+),").          // battery
	Number("(d+),").             // gsm
	Number("(d+.?d*),").         // odometer
	Number("(d+),").             // gps
	Any().
	Compile()

var patternNew = kit.NewPattern().
	Text("$$").
	Number("(d+),").             // imei
	Number("(d+),").             // event
	Number("(dddd)/(dd)/(dd),"). // date (yyyy/mm/dd)
	Number("(dd):(dd):(dd),").   // time (hh:mm:ss)
	Number("(-?d+.?d*),").       // latitude
	Number("(-?d+.?d*),").       // longitude
	Number("(-?d+.?d*),").       // altitude
	Number("(d+.?d*),").         // speed
	Number("d+.?d*,").           // acceleration
	Number("d+.?d*,").           // deceleration
	Number("d+,").
	Number("(d+.?d*),"). // course
	Number("(d+),").     // satellites
	Number("(d+.?d*),"). // hdop
	Number("(d+.?d*),"). // odometer
	Number("(d+.?d*),"). // fuel consumption
	Number("(d+.d+),").  // battery
	Number("(d+),").     // gsm
	Number("(d+),").     // gps
	GroupBegin().
	Number("d,").          // reset mode
	Expression("([01])").  // input 1
	Expression("([01])").  // input 1
	Expression("([01])").  // input 1
	Expression("([01]),"). // output 1
	Number("(d+.?d*),").   // adc 1
	Number("(d+.?d*),").   // fuel level
	Number("d+,").         // engine load
	Number("(d+),").       // engine hours
	Number("(d+),").       // oil pressure
	Number("(d+),").       // oil level
	Number("(-?d+),").     // oil temperature
	Number("(d+),").       // coolant pressure
	Number("(d+),").       // coolant level
	Number("(-?d+)").      // coolant temperature
	GroupEndWith("?").
	Any().
	Compile()

var patternAck = regexp.MustCompile(`^\$\$\d+,(\d+),.*,(\d+)##$`)

func decodeEvent(pos *model.Position, event int) {
	pos.Set(model.KeyEvent, event)
	switch event {
	case 4001, 4003, 6011, 6013:
		pos.Set(model.KeyIgnition, true)
	case 4002, 4004, 6012, 6014:
		pos.Set(model.KeyIgnition, false)
	case 4005:
		pos.Set(model.KeyCharge, false)
	case 6002:
		pos.AddAlarm(model.AlarmOverspeed)
	case 6006:
		pos.AddAlarm(model.AlarmAcceleration)
	case 6007:
		pos.AddAlarm(model.AlarmBraking)
	case 6008:
		pos.AddAlarm(model.AlarmLowPower)
	case 6009:
		pos.AddAlarm(model.AlarmPowerCut)
	case 6010:
		pos.AddAlarm(model.AlarmPowerRestored)
	case 6016:
		pos.AddAlarm(model.AlarmIdle)
	case 6017:
		pos.AddAlarm(model.AlarmTow)
	case 6030, 6071:
		pos.Set(model.KeyMotion, true)
	case 6031:
		pos.Set(model.KeyMotion, false)
	case 6032:
		pos.AddAlarm(model.AlarmParking)
	case 6090:
		pos.AddAlarm(model.AlarmRemoving)
	case 6091:
		pos.AddAlarm(model.AlarmLowBattery)
	}
}

// Decode decodes one message.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if d.Datagram {
		if match := patternAck.FindStringSubmatch(sentence); match != nil {
			m.Send("!UDP_ACK," + match[1] + "," + match[2])
		}
	}

	if d.Form != nil {
		return d.decodeCustom(m, sentence)
	}
	return d.decodeFixed(m, sentence)
}

func (d *Decoder) decodeCustom(m *ingest.Message, sentence string) any {
	keys := kit.Split(*d.Form, ",")
	values := kit.Split(strings.ReplaceAll(strings.ReplaceAll(sentence, "$$", ""), "##", ""), ",")

	if len(values) < len(keys) {
		return nil
	}

	pos := m.NewPosition()
	dateBuilder := kit.NewDateBuilder(nil)

	for i, key := range keys {
		value := values[i]
		switch key {
		case "UID", "IM":
			if ds := m.Session(value); ds != nil {
				pos.DeviceID = ds.DeviceID
			}
		case "EV":
			decodeEvent(pos, kit.Int(value))
		case "D":
			date := kit.Split(value, "/")
			dateBuilder.SetMonth(kit.Int(date[0]))
			dateBuilder.SetDay(kit.Int(date[1]))
			dateBuilder.SetYear(kit.Int(date[2]))
		case "T":
			t := kit.Split(value, ":")
			dateBuilder.SetHour(kit.Int(t[0]))
			dateBuilder.SetMinute(kit.Int(t[1]))
			dateBuilder.SetSecond(kit.Int(t[2]))
		case "LT":
			pos.SetLatitude(kit.Double(value))
		case "LN":
			pos.SetLongitude(kit.Double(value))
		case "AL":
			pos.Altitude = float64(kit.Int(value))
		case "GSPT":
			pos.Speed = units.KnotsFromKph(kit.Double(value))
		case "HD":
			if strings.Contains(value, ".") {
				pos.Course = kit.Double(value)
			} else {
				pos.Course = float64(kit.Int(value)) / 10.0
			}
		case "SV":
			pos.Set(model.KeySatellites, kit.Int(value))
		case "BV":
			pos.Set(model.KeyBattery, kit.Double(value))
		case "CQ":
			pos.Set(model.KeyRssi, kit.Int(value))
		case "MI":
			pos.Set(model.KeyOdometer, kit.Int(value))
		case "GS":
			pos.Valid = kit.Int(value) == 3
		case "SI":
			pos.Set(model.KeyIccid, value)
		case "IG":
			if ignition := kit.Int(value); ignition > 0 {
				pos.Set(model.KeyIgnition, ignition == 1)
			}
		case "OT":
			pos.Set(model.KeyOutput, kit.Int(value))
		}
	}

	pos.SetTime(dateBuilder.Date())

	if pos.DeviceID > 0 {
		return pos
	}
	return nil
}

func (d *Decoder) decodeFixed(m *ingest.Message, sentence string) any {
	var parser *kit.Parser
	if d.newFormat == nil {
		parser = kit.NewParser(patternNew, sentence)
		if parser.Matches() {
			d.newFormat = new(true)
		} else {
			parser = kit.NewParser(patternOld, sentence)
			if !parser.Matches() {
				return nil
			}
			d.newFormat = new(false)
		}
	} else {
		if *d.newFormat {
			parser = kit.NewParser(patternNew, sentence)
		} else {
			parser = kit.NewParser(patternOld, sentence)
		}
		if !parser.Matches() {
			return nil
		}
	}
	newFormat := *d.newFormat

	pos := m.NewPosition()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	decodeEvent(pos, parser.MustInt())

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Speed = units.KnotsFromMph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextDouble())

	if newFormat {
		pos.Set(model.KeyOdometer, units.MetersFromMiles(parser.NextDoubleDefault(0)))
		pos.Set(model.KeyFuelConsumption, parser.NextNullable())
	}

	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0))
	pos.Set(model.KeyRssi, parser.NextDouble())

	if !newFormat {
		pos.Set(model.KeyOdometer, units.MetersFromMiles(parser.NextDoubleDefault(0)))
	}

	pos.Valid = parser.NextIntDefault(0) == 1

	if newFormat && parser.HasNextN(13) {
		pos.Set(model.PrefixIn+"1", parser.NextInt())
		pos.Set(model.PrefixIn+"2", parser.NextInt())
		pos.Set(model.PrefixIn+"3", parser.NextInt())
		pos.Set(model.PrefixOut+"1", parser.NextInt())
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.KeyFuel, parser.NextDouble())
		pos.Set(model.KeyHours, units.MsFromHours(int64(parser.MustInt())))
		pos.Set("oilPressure", parser.NextInt())
		pos.Set("oilLevel", parser.NextInt())
		pos.Set("oilTemp", parser.NextInt())
		pos.Set("coolantPressure", parser.NextInt())
		pos.Set("coolantLevel", parser.NextInt())
		pos.Set("coolantTemp", parser.NextInt())
	}

	return pos
}

// encode is XirgoProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	if c.Type == model.CommandOutputControl {
		return kit.Format("+XT:7005,%d,1", kit.CommandInt(c, model.CommandKeyData)+1)
	}
	return nil
}
