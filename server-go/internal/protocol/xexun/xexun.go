// Package xexun is the Xexun protocol (XexunProtocol): GPRMC sentences with the IMEI over
// TCP, framed from the sentence (or by lines when xexun.extended is set).
package xexun

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server; xexun.extended selects the full
// format.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	full := cfg.Bool("xexun.extended", false)
	return ingest.Protocol{
		Name:         "xexun",
		DataCommands: protocol.DataCommands("xexun"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			var framer ingest.Framer = Framer()
			if full {
				framer = ingest.NewLineFramer(ingest.MaxFrameLength) // tracker bug \n\r
			}
			return ingest.Pipeline{
				Framer:  framer,
				Text:    true,
				TextOut: true,
				Decoder: &Decoder{Full: full},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}, nil
}

// Framer is XexunFrameDecoder: from "GPRMC" (or "GNRMC") to the comma after "imei:".
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 80 {
			return nil
		}
		beginIndex := buf.IndexOfBytes([]byte("GPRMC"))
		if beginIndex == -1 {
			beginIndex = buf.IndexOfBytes([]byte("GNRMC"))
			if beginIndex == -1 {
				return nil
			}
		}
		identifierIndex := buf.IndexOfIn("imei:", beginIndex, buf.WriterIndex())
		if identifierIndex == -1 {
			return nil
		}
		endIndex := buf.IndexOf(identifierIndex, buf.WriterIndex(), ',')
		if endIndex == -1 {
			return nil
		}
		buf.Skip(beginIndex - buf.ReaderIndex())
		return buf.ReadSlice(endIndex - beginIndex + 1)
	}}
}

var basic = kit.NewPattern().
	Expression("G[PN]RMC,").
	Number("(?:(dd)(dd)(dd))?.?d*,"). // time (hhmmss)
	Expression("([AV]),").            // validity
	Number("(d*?)(d?d.d+),([NS]),").  // latitude
	Number("(d*?)(d?d.d+),([EW])?,"). // longitude
	Number("(d+.?d*),").              // speed
	Number("(d+.?d*)?,").             // course
	Number("(?:(dd)(dd)(dd))?,").     // date (ddmmyy)
	Expression("[^*]*").Text("*").
	Number("xx"). // checksum
	Expression(`\r\n`).Optional().
	Expression(",([FL]),").            // signal
	Expression("([^,]*),").Optional(). // alarm
	Any().
	Number("imei:(d+),"). // imei
	String()

var (
	patternBasic = kit.CompilePattern(basic)

	patternFull = kit.NewPattern().
			Any().
			Number("(d+),").         // serial
			Expression("([^,]+)?,"). // phone number
			Expression(basic).
			Number("(d+),").         // satellites
			Number("(-?d+.d+)?,").   // altitude
			Number("[FL]:(d+.d+)V"). // power
			Any().
			Compile()
)

// Decoder is XexunProtocolDecoder; Full is the extended format.
type Decoder struct {
	Full bool
}

func decodeStatus(pos *model.Position, value *string) {
	if value == nil {
		return
	}
	switch strings.ToLower(*value) {
	case "acc on", "accstart":
		pos.Set(model.KeyIgnition, true)
	case "acc off", "accstop":
		pos.Set(model.KeyIgnition, false)
	case "help me!", "help me":
		pos.AddAlarm(model.AlarmSos)
	case "low battery":
		pos.AddAlarm(model.AlarmLowBattery)
	case "move!", "moved!":
		pos.AddAlarm(model.AlarmMovement)
	}
}

// Decode decodes one sentence.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	pattern := patternBasic
	if d.Full {
		pattern = patternFull
	}

	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	if d.Full {
		pos.Set("serial", parser.NextNullable())
		pos.Set("number", parser.NextNullable())
	}

	dateBuilder := kit.NewDateBuilder(nil).
		SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	pos.Speed = m.ConvertSpeed(parser.NextDoubleDefault(0), "kn")

	pos.Course = parser.NextDoubleDefault(0)

	dateBuilder.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(dateBuilder.Date())

	pos.Set("signal", parser.NextNullable())

	decodeStatus(pos, parser.NextNullable())

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	if d.Full {
		pos.Set(model.KeySatellites, parser.NextInt())

		pos.Altitude = parser.NextDoubleDefault(0)

		pos.Set(model.KeyPower, parser.NextDoubleDefault(0))
	}

	return pos
}

// encode is XexunProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	if !c.Attributes.Has(model.CommandKeyDevicePassword) { // initDevicePassword(command, "123456")
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), "123456"))
	}
	switch c.Type {
	case model.CommandEngineStop:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "powercar%s 11", model.CommandKeyDevicePassword)
	case model.CommandEngineResume:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "powercar%s 00", model.CommandKeyDevicePassword)
	}
	return nil
}
