// Package pst is the PST protocol (PstProtocol): binary messages over UDP, and over TCP
// in "(" ... ")" frames with 0x27 escaping.
package pst

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a UDP server and a framed TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "pst",
		DataCommands: protocol.DataCommands("pst"),
		Servers: []ingest.Server{
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:       Framer(),
					FrameEncoder: FrameEncode,
					Decoder:      ingest.DecoderFunc(decode),
					Encoder:      ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}
}

// Framer is PstFrameDecoder: leading '(' are skipped, the frame ends at ')', and 0x27
// escapes the next byte (xor 0x40).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		for buf.IsReadable() && buf.GetByte(buf.ReaderIndex()) == 0x28 {
			buf.Skip(1)
		}
		endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0x29)
		if endIndex <= 0 {
			return nil
		}
		result := kit.Empty()
		for buf.ReaderIndex() < endIndex {
			b := buf.ReadUnsignedByte()
			if b == 0x27 {
				b = buf.ReadUnsignedByte() ^ 0x40
			}
			result.WriteByte8(b)
		}
		buf.Skip(1)
		return result
	}}
}

// FrameEncode is PstFrameEncoder: the message in parentheses, with 0x27, 0x28 and 0x29
// escaped.
func FrameEncode(msg []byte) []byte {
	out := []byte{'('}
	for _, b := range msg {
		if b == 0x27 || b == 0x28 || b == 0x29 {
			out = append(out, 0x27, b^0x40)
		} else {
			out = append(out, b)
		}
	}
	return append(out, ')')
}

const (
	msgAck     = 0x00
	msgStatus  = 0x05
	msgCommand = 0x06
)

func readDate(buf *kit.Buf) time.Time {
	value := buf.ReadUnsignedInt()
	return kit.NewDateBuilder(nil).
		SetYear(int(kit.Between(value, 26, 32))).
		SetMonth(int(kit.Between(value, 22, 26))).
		SetDay(int(kit.Between(value, 17, 22))).
		SetHour(int(kit.Between(value, 12, 17))).
		SetMinute(int(kit.Between(value, 6, 12))).
		SetSecond(int(kit.Between(value, 0, 6))).Date()
}

func readCoordinate(buf *kit.Buf) float64 {
	value := buf.ReadUnsignedInt()
	sign := 1.0
	if kit.Check(value, 31) {
		sign = -1
	}
	value = kit.To(value, 31)
	return sign * (float64(kit.From(value, 16)) + float64(kit.To(value, 16))/10000.0) / 60
}

func sendResponse(m *ingest.Message, id int64, version int, index int64, kind int) {
	response := kit.Empty()
	response.WriteInt(id)
	response.WriteByte8(version)
	response.WriteInt(index)
	response.WriteByte8(msgAck)
	response.WriteByte8(kind)
	response.WriteShort(kit.CRC16(kit.CRC16_XMODEM, response.Bytes()))
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	id := buf.ReadUnsignedInt()
	ds := m.Session(strconv.FormatInt(id, 10))
	if ds == nil {
		return nil
	}

	version := buf.ReadUnsignedByte()
	index := buf.ReadUnsignedInt()

	kind := buf.ReadUnsignedByte()

	sendResponse(m, id, version, index, kind)

	if kind != msgStatus {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.DeviceTime = model.NewTime(readDate(buf))

	status := buf.ReadUnsignedByte()
	pos.Set(model.KeyBlocked, kit.Check(int64(status), 4))
	pos.Set(model.KeyIgnition, kit.Check(int64(status), 7))
	pos.Set(model.KeyStatus, status)

	count := buf.ReadUnsignedByte()
	for range count {
		tag := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()

		switch tag {
		case 0x09:
			buf.ReadUnsignedByte() // sensor count
			buf.ReadUnsignedByte() // sensor logic
			buf.ReadUnsignedByte() // sensor status
		case 0x0D:
			if battery := buf.ReadUnsignedByte(); battery <= 20 {
				pos.Set(model.KeyBatteryLevel, battery*5)
			}
		case 0x10:
			pos.Valid = true
			pos.FixTime = model.NewTime(readDate(buf))
			pos.SetLatitude(readCoordinate(buf))
			pos.SetLongitude(readCoordinate(buf))
			pos.Speed = float64(buf.ReadUnsignedByte())
			pos.Course = float64(buf.ReadUnsignedByte() * 2)
			pos.Altitude = float64(buf.ReadShort())
			buf.ReadUnsignedInt() // gps condition
		default:
			buf.Skip(length)
		}
	}

	if pos.FixTime.IsZero() {
		return nil
	}
	return pos
}

func encodeContent(m *ingest.Message, deviceID int64, kind, data1, data2 int) *kit.Buf {
	buf := kit.Empty()

	buf.WriteInt(kit.Long(m.UniqueID(deviceID)))
	buf.WriteByte8(0x06) // version

	buf.WriteInt(1) // index
	buf.WriteByte8(msgCommand)
	buf.WriteShort(kind)
	buf.WriteShort(data1)
	buf.WriteShort(data2)

	buf.WriteShort(kit.CRC16(kit.CRC16_XMODEM, buf.Bytes()))

	return buf
}

// encode is PstProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return encodeContent(m, c.DeviceID, 0x0002, 0xffff, 0xffff)
	case model.CommandEngineResume:
		return encodeContent(m, c.DeviceID, 0x0001, 0xffff, 0xffff)
	}
	return nil
}
