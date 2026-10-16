// Package enfora is the Enfora protocol (EnforaProtocol): binary messages carrying an IMEI
// and an NMEA GPRMC sentence, over TCP and UDP.
package enfora

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: TCP (length-framed) and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "enfora",
		DataCommands: protocol.DataCommands("enfora"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 0, 2, -2, 2),
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}
}

var pattern = kit.NewPattern().
	Text("GPRMC,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+)?,").     // speed
	Number("(d+.d+)?,").     // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Any().
	Compile()

const imeiLength = 15

func isDigit(b int8) bool { return b >= '0' && b <= '9' }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	// Find the IMEI; like Java, a match at index 0 doesn't end the search.
	index := -1
	for i := buf.ReaderIndex(); i < buf.WriterIndex()-imeiLength; i++ {
		index = i
		for j := i; j < i+imeiLength; j++ {
			if !isDigit(buf.GetByte(j)) {
				index = -1
				break
			}
		}
		if index > 0 {
			break
		}
	}
	if index == -1 {
		return nil
	}

	ds := m.Session(buf.GetLatin1(index, imeiLength))
	if ds == nil {
		return nil
	}

	// Find the NMEA sentence
	start := buf.IndexOfIn("GPRMC", buf.ReaderIndex(), buf.WriterIndex())
	if start == -1 {
		return nil
	}

	parser := kit.NewParser(pattern, buf.GetLatin1(start, buf.ReadableBytes()-start))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(b.Date())
	return pos
}

func encodeContent(content string) *kit.Buf {
	buf := kit.Empty()
	buf.WriteShort(kit.Len(content) + 6)
	buf.WriteShort(0)    // index
	buf.WriteByte8(0x04) // command type
	buf.WriteByte8(0)    // optional header
	buf.WriteBytes(kit.ASCIIBytes(content))
	return buf
}

// encode is EnforaProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: data")
		}
		return encodeContent(*data)
	case model.CommandEngineStop:
		return encodeContent("AT$IOGP3=1")
	case model.CommandEngineResume:
		return encodeContent("AT$IOGP3=0")
	}
	return nil
}
