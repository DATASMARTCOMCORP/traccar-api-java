// Package wondex is the Wondex protocol (WondexProtocol): CSV text lines and binary
// keep-alives over TCP and UDP.
package wondex

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP and a UDP server. The same encoder also
// encodes text (SMS) commands.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "wondex",
		DataCommands: protocol.DataCommands("wondex"),
		TextEncoder:  ingest.CommandEncoderFunc(encode),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  Framer(),
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}
}

const keepAliveLength = 8

// Framer is WondexFrameDecoder: an 8-byte keep-alive (0xD0, echoed back) or a CRLF line.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(m *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < keepAliveLength {
			return nil
		}
		if buf.GetUnsignedByte(buf.ReaderIndex()) == 0xD0 {
			frame := buf.ReadSlice(keepAliveLength)
			if m.HasChannel() {
				m.Send(kit.NewBuf(append([]byte(nil), frame.Bytes()...)))
			}
			return frame
		}
		if index := buf.IndexOfBytes([]byte("\r\n")); index != -1 {
			frame := buf.ReadSlice(index - buf.ReaderIndex())
			buf.Skip(2)
			return frame
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Number("[^d]*").                // header
	Number("(d+),").                // device identifier
	Number("(dddd)(dd)(dd)").       // date (yyyymmdd)
	Number("(dd)(dd)(dd),").        // time (hhmmss)
	Number("(-?d+.d+),").           // longitude
	Number("(-?d+.d+),").           // latitude
	Number("(d+),").                // speed
	Number("(d+),").                // course
	Number("(-?d+.?d*),").          // altitude
	Number("(d+),").                // satellites
	Number("(d+),?").               // event
	Number("(d+.d+)V,").Optional(). // battery
	Number("(d+.d+)?,?").           // odometer
	Number("(d+)?,?").              // input
	Number("(d+.d+)?,?").           // adc1
	Number("(d+.d+)?,?").           // adc2
	Number("(d+)?").                // output
	Any().
	Compile()

// decode is WondexProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.GetUnsignedByte(0) == 0xD0 {
		deviceID := buf.GetUnsignedIntLE(4) // the high half of the little-endian long
		m.Session(kit.Format("%d", deviceID))
		return nil
	}

	text := kit.Latin1(buf.Bytes())
	if strings.HasPrefix(text, "$OK:") || strings.HasPrefix(text, "$ERR:") || strings.HasPrefix(text, "$MSG:") {

		ds := m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		now := m.Now
		m.LastLocation(pos, &now)
		pos.Set(model.KeyResult, text)

		return pos
	}

	parser := kit.NewParser(pattern, text)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)

	satellites := parser.NextIntDefault(0)
	pos.Valid = satellites != 0
	pos.Set(model.KeySatellites, satellites)

	pos.Set(model.KeyEvent, parser.NextNullable())
	pos.Set(model.KeyBattery, parser.NextDouble())
	if parser.HasNext() {
		pos.Set(model.KeyOdometer, parser.NextDoubleDefault(0)*1000)
	}
	pos.Set(model.KeyInput, parser.NextNullable())
	pos.Set(model.PrefixAdc+"1", parser.NextNullable())
	pos.Set(model.PrefixAdc+"2", parser.NextNullable())
	pos.Set(model.KeyOutput, parser.NextNullable())

	return pos
}

// encode is WondexProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	if !c.Attributes.Has(model.CommandKeyDevicePassword) { // initDevicePassword(command, "0000")
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), "0000"))
	}
	format := func(f string) string {
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), f, model.CommandKeyDevicePassword)
	}
	switch c.Type {
	case model.CommandRebootDevice:
		return format("$WP+REBOOT=%s")
	case model.CommandGetDeviceStatus:
		return format("$WP+TEST=%s")
	case model.CommandGetModemStatus:
		return format("$WP+GSMINFO=%s")
	case model.CommandIdentification:
		return format("$WP+IMEI=%s")
	case model.CommandPositionSingle:
		return format("$WP+GETLOCATION=%s")
	case model.CommandGetVersion:
		return format("$WP+VER=%s")
	}
	return nil
}
