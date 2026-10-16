// Package orion is the Orion protocol (OrionProtocol): binary user logs (positions) and
// system logs.
package orion

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "orion",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgUserlog = 0
	msgSyslog  = 3
)

// Framer is OrionFrameDecoder: a user log's length is the sum of its records', a system
// log's is at offset 8.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		length := 6
		if buf.ReadableBytes() < length {
			return nil
		}
		kind := buf.GetUnsignedByte(buf.ReaderIndex()+2) & 0x0f
		if kind == msgUserlog && buf.ReadableBytes() >= length+5 {
			index := buf.ReaderIndex() + 3
			count := buf.GetUnsignedByte(index) & 0x0f
			index += 5
			length += 5
			for range count {
				if buf.ReadableBytes() < length {
					return nil
				}
				logLength := buf.GetUnsignedByte(index + 1)
				index += logLength
				length += logLength
			}
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		} else if kind == msgSyslog && buf.ReadableBytes() >= length+12 {
			length += buf.GetUnsignedShortLE(buf.ReaderIndex() + 8)
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		}
		return nil
	}}
}

func sendResponse(m *ingest.Message, buf *kit.Buf) {
	response := kit.Empty()
	response.WriteByte8('*')
	response.WriteShort(buf.GetUnsignedShort(buf.WriterIndex() - 2))
	response.WriteByte8(buf.GetUnsignedByte(buf.WriterIndex() - 3))
	m.Send(response)
}

func convertCoordinate(raw int32) float64 {
	degrees := raw / 1000000
	minutes := float64(raw%1000000) / 10000.0
	return float64(degrees) + minutes/60
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	if buf.ReadUnsignedByte()&0x0f != msgUserlog {
		return nil
	}

	header := buf.ReadUnsignedByte()
	if header&0x40 != 0 {
		sendResponse(m, buf)
	}

	ds := m.Session(strconv.FormatInt(buf.ReadUnsignedInt(), 10))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	for range header & 0x0f {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
		buf.ReadUnsignedByte() // length
		pos.Set(model.KeyFlags, buf.ReadUnsignedShortLE())

		pos.SetLatitude(convertCoordinate(buf.ReadIntLE()))
		pos.SetLongitude(convertCoordinate(buf.ReadIntLE()))
		pos.Altitude = float64(buf.ReadShortLE()) / 10.0
		pos.Course = float64(buf.ReadUnsignedShortLE())
		pos.Speed = float64(buf.ReadUnsignedShortLE()) * 0.0539957

		date := kit.NewDateBuilder(nil).
			SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		pos.SetTime(date.Date())

		satellites := buf.ReadUnsignedByte()
		pos.Valid = satellites >= 3
		pos.Set(model.KeySatellites, satellites)

		positions = append(positions, pos)
	}
	return positions
}
