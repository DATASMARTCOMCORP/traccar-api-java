// Package thinkrace is the ThinkRace protocol (ThinkRaceProtocol): binary messages over
// TCP.
package thinkrace

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "thinkrace",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2+12+1+1, 2, 2, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgLogin = 0x80
	msgGPS   = 0x90
)

func convertCoordinate(raw int64, negative bool) float64 {
	degrees := raw / 1000000
	minutes := float64(raw%1000000) / 10000.0
	result := float64(degrees) + minutes/60
	if negative {
		result = -result
	}
	return result
}

// decode is ThinkRaceProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	id := buf.ReadSlice(12)
	buf.ReadUnsignedByte() // separator
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length

	switch typ {
	case msgLogin:

		command := buf.ReadUnsignedByte() // 0x00 - heartbeat

		if command == 0x01 {
			imei := buf.GetString(buf.ReaderIndex(), 15)
			ds := m.Session(imei)
			if ds != nil && m.HasChannel() {
				response := kit.Empty()
				response.WriteByte8(0x48)
				response.WriteByte8(0x52) // header
				response.WriteBuf(id)
				response.WriteByte8(0x2c) // separator
				response.WriteByte8(typ)
				response.WriteShort(0x0002) // length
				response.WriteShort(0x8000)
				response.WriteShort(0x0000) // checksum
				m.Send(response)
			}
		}

	case msgGPS:

		ds := m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

		flags := int64(buf.ReadUnsignedByte())

		pos.Valid = true
		pos.SetLatitude(convertCoordinate(buf.ReadUnsignedInt(), !kit.Check(flags, 0)))
		pos.SetLongitude(convertCoordinate(buf.ReadUnsignedInt(), !kit.Check(flags, 1)))

		pos.Speed = float64(buf.ReadUnsignedByte())
		pos.Course = float64(buf.ReadUnsignedByte())

		lac := buf.ReadUnsignedShort()
		pos.Network = model.NetworkOf(m.CellTowerFromLacCid(lac, int64(buf.ReadUnsignedShort())))

		return pos

	}

	return nil
}
