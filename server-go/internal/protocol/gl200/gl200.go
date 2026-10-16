// Package gl200 is the Queclink GL200 protocol (Gl200Protocol): "+RESP:GT..." text reports
// and their binary forms, over TCP and UDP.
package gl200

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: TCP (framed) and UDP servers.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	opts := ReadOptions(cfg, "gl200")
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{
				TextOut: true,
				Decoder: &Decoder{Text: &TextDecoder{Options: opts}},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
			if framer {
				pl.Framer = Framer()
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "gl200",
		DataCommands: protocol.DataCommands("gl200"),
		Servers:      []ingest.Server{{NewPipeline: pipeline(true)}, {Datagram: true, NewPipeline: pipeline(false)}},
	}, nil
}

// Options are the text decoder's settings: <protocol>.ignoreFixTime and <protocol>.ack.
type Options struct {
	IgnoreFixTime bool
	Ack           bool
}

// ReadOptions reads the options of the protocol name from the configuration.
func ReadOptions(cfg *config.Config, name string) Options {
	return Options{
		IgnoreFixTime: cfg.Bool(name+".ignoreFixTime", false),
		Ack:           cfg.Bool(name+".ack", false),
	}
}

var binaryHeaders = map[string]bool{
	"+RSP": true, "+BSP": true, "+EVT": true, "+BVT": true, "+INF": true,
	"+BNF": true, "+HBD": true, "+CRD": true, "+BRD": true, "+LGN": true,
}

// IsBinary is Gl200FrameDecoder.isBinary.
func IsBinary(buf *kit.Buf) bool {
	if buf.GetByte(buf.ReaderIndex()+1) == 0 {
		return true
	}
	header := buf.GetLatin1(buf.ReaderIndex(), 4)
	if header == "+ACK" {
		return buf.GetByte(buf.ReaderIndex()+4) != ':'
	}
	return binaryHeaders[header]
}

const minimumLength = 11

// Framer is Gl200FrameDecoder: binary frames carry their length (where depends on the
// header); text frames end with '$' (or a zero byte), which is dropped.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < minimumLength {
			return nil
		}
		r := buf.ReaderIndex()
		if IsBinary(buf) {
			var length int
			if buf.GetByte(r+1) == 0 {
				length = buf.GetUnsignedShort(r + 2)
			} else {
				switch buf.GetLatin1(r, 4) {
				case "+ACK":
					length = buf.GetUnsignedByte(r + 6)
				case "+INF", "+BNF":
					length = buf.GetUnsignedShort(r + 7)
				case "+HBD":
					length = buf.GetUnsignedByte(r + 5)
				case "+CRD", "+BRD", "+LGN":
					length = buf.GetUnsignedShort(r + 6)
				default:
					length = buf.GetUnsignedShort(r + 9)
				}
			}
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
			return nil
		}
		endIndex := buf.IndexOf(r, buf.WriterIndex(), '$')
		if endIndex < 0 {
			endIndex = buf.IndexOf(r, buf.WriterIndex(), 0)
		}
		if endIndex > 0 {
			frame := buf.ReadSlice(endIndex - r)
			buf.Skip(1) // delimiter
			return frame
		}
		return nil
	}}
}

// Decoder is Gl200ProtocolDecoder: binary messages go to the binary decoder, the others
// to the text decoder.
type Decoder struct {
	Text *TextDecoder
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	if IsBinary(msg.(*kit.Buf)) {
		return DecodeBinary(m, msg)
	}
	return d.Text.Decode(m, msg)
}

// encode is Gl200ProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	if !c.Attributes.Has(model.CommandKeyDevicePassword) { // initDevicePassword(command, "")
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), ""))
	}
	f := func(format string) any {
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), format, model.CommandKeyDevicePassword)
	}
	switch c.Type {
	case model.CommandPositionSingle:
		return f("AT+GTRTO=%s,1,,,,,,FFFF$")
	case model.CommandEngineStop:
		return f("AT+GTOUT=%s,1,,,0,0,0,0,0,0,0,,,,,,,FFFF$")
	case model.CommandEngineResume:
		return f("AT+GTOUT=%s,0,,,0,0,0,0,0,0,0,,,,,,,FFFF$")
	case model.CommandIdentification:
		return f("AT+GTRTO=%s,8,,,,,,FFFF$")
	case model.CommandRebootDevice:
		return f("AT+GTRTO=%s,3,,,,,,FFFF$")
	}
	return nil
}
