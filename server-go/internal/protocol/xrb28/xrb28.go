// Package xrb28 is the Xrb28 protocol (Xrb28Protocol): "*SCOR,..#" text lines over TCP,
// with replies and commands sent in ISO-8859-1.
package xrb28

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "xrb28",
		DataCommands: protocol.DataCommands("xrb28"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			return ingest.Pipeline{
				Framer:       ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:         true,
				TextOut:      true,
				Decoder:      d,
				Encoder:      &Encoder{Decoder: d},
				FrameEncoder: latin1,
			}
		}}},
	}
}

// latin1 re-encodes the UTF-8 that TextOut writes in ISO-8859-1, the charset of the
// protocol's StringEncoder: characters above U+00FF become '?', as the JDK encoder
// replaces them.
func latin1(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		b = b[size:]
		if r > 0xFF {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

// Decoder is Xrb28ProtocolDecoder; PendingCommand is the type of the last engine stop or
// alarm disarm command sent, answered on the device's next R0 message.
type Decoder struct {
	PendingCommand *string
}

var pattern = kit.NewPattern().
	Text("*").
	Expression("....,").
	Expression("..,").           // vendor
	Number("d{15},").            // imei
	Number("d{12},").Optional(). // time
	Expression("..,").           // type
	Number("[01],").             // reserved
	Number("(dd)(dd)(dd).d+,").  // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(d{2,3})(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+),").         // satellites
	Number("(d+.d+),").      // hdop
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(-?d+.?d*),").   // altitude
	Expression(".,").        // height unit
	Expression(".#").        // mode
	Compile()

// Java's "X$" also matches before a final line terminator.
var (
	trailingHash = regexp.MustCompile("#((?:\r\n|[\n\r\u0085  ])?)$")
	trailingR    = regexp.MustCompile("R((?:\r\n|[\n\r\u0085  ])?)$")
	replyTypes   = regexp.MustCompile("^(?:L0|L1|W0|E1)$")
)

// Decode decodes one line.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	values := kit.Split(trailingHash.ReplaceAllString(sentence, "${1}"), ",")

	index := 0
	header := values[index]
	index++
	vendor := values[index]
	index++

	imei := values[index]
	index++
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	var time *string
	if kit.Len(values[index]) == 12 {
		time = &values[index]
		index++
	}

	typ := values[index]
	index++

	var response strings.Builder
	response.WriteString("ÿÿ")
	response.WriteString(trailingR.ReplaceAllString(header, "S${1}") + ",")
	response.WriteString(vendor + ",")
	response.WriteString(imei + ",")
	if time != nil {
		response.WriteString(*time + ",")
	}
	if replyTypes.MatchString(typ) {
		response.WriteString(typ + "#\n")
		m.Send(response.String())
	} else if typ == "R0" && d.PendingCommand != nil {
		command := "L0"
		if *d.PendingCommand == model.CommandAlarmArm {
			command = "L1"
		}
		response.WriteString(command)
		response.WriteString(strings.Join(values[index:], ","))
		response.WriteString("#\n")
		m.Send(response.String())
		d.PendingCommand = nil
	}

	if !strings.HasPrefix(typ, "D") {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		next := func() int {
			v := kit.Int(values[index])
			index++
			return v
		}

		switch typ {
		case "Q0":
			pos.Set(model.KeyBattery, float64(next())/100.0)
			pos.Set(model.KeyBatteryLevel, next())
			pos.Set(model.KeyRssi, next())
		case "H0":
			pos.Set(model.KeyBlocked, next() > 0)
			pos.Set(model.KeyBattery, float64(next())/100.0)
			pos.Set(model.KeyRssi, next())
			pos.Set(model.KeyBatteryLevel, next())
		case "W0":
			switch next() {
			case 1:
				pos.AddAlarm(model.AlarmMovement)
			case 2:
				pos.AddAlarm(model.AlarmFallDown)
			case 3:
				pos.AddAlarm(model.AlarmLowBattery)
			}
		case "E0":
			pos.AddAlarm(model.AlarmFault)
			pos.Set("error", next())
		case "S1":
			pos.Set(model.KeyEvent, next())
		case "R0", "L0", "L1", "S4", "S5", "S6", "S7", "V0", "G0", "K0", "I0", "M0":
			pos.Set(model.KeyResult, strings.Join(values[index:], ","))
		}

		if pos.Attributes.Len() > 0 {
			return pos
		}
		return nil
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	dateBuilder := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextDouble())

	dateBuilder.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(dateBuilder.Date())

	pos.Altitude = parser.MustDouble()

	return pos
}

// Encoder is Xrb28ProtocolEncoder; Decoder is the connection's decoder (nil when there
// is no channel), which engine stop and alarm disarm commands leave pending.
type Encoder struct {
	Decoder *Decoder
}

func formatCommand(m *ingest.Message, c *model.Command, content string) string {
	return "ÿÿ*SCOS,OM," + m.UniqueID(c.DeviceID) + "," + content + "#\n"
}

// Encode encodes a command.
func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		return formatCommand(m, c, javaString(kit.CommandString(c, model.CommandKeyData)))
	case model.CommandPositionSingle:
		return formatCommand(m, c, "D0")
	case model.CommandPositionPeriodic:
		return formatCommand(m, c, "D1,"+strconv.Itoa(kit.CommandInt(c, model.CommandKeyFrequency)))
	case model.CommandEngineStop, model.CommandAlarmDisarm:
		if e.Decoder != nil {
			t := c.Type
			e.Decoder.PendingCommand = &t
		}
		return formatCommand(m, c, "R0,0,20,1234,"+strconv.FormatInt(m.Now.UnixMilli()/1000, 10))
	}
	return nil
}

// javaString is String.format's %s of a nullable string.
func javaString(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}
