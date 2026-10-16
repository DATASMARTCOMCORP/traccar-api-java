// Package nvs is the NVS protocol (NvsProtocol): binary records over TCP after an IMEI
// login.
package nvs

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "nvs",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is NvsFrameDecoder: a login (first byte 0) has a 2-byte length; data has a
// 4-byte marker, a length and a 2-byte checksum.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 4+2 {
			return nil
		}
		var length int
		if buf.GetUnsignedByte(buf.ReaderIndex()) == 0 {
			length = 2 + buf.GetUnsignedShort(buf.ReaderIndex())
		} else {
			length = 4 + 2 + buf.GetUnsignedShort(buf.ReaderIndex()+4) + 2
		}
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.GetUnsignedByte(buf.ReaderIndex()) == 0 {
		buf.ReadUnsignedShort() // length
		imei := buf.GetString(buf.ReaderIndex(), 15)
		response := "NO01"
		if m.Session(imei) != nil {
			response = "OK"
		}
		m.Send(kit.NewBuf(kit.NettyBytes(response)))
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}
	positions := []*model.Position{}
	buf.Skip(4)             // marker
	buf.ReadUnsignedShort() // length
	buf.ReadLong()          // imei
	buf.ReadUnsignedByte()  // codec
	count := buf.ReadUnsignedByte()
	for i := 0; i < count; i++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		pos.Set("reason", buf.ReadUnsignedByte())
		pos.SetLongitude(float64(buf.ReadInt()) / 10000000.0)
		pos.SetLatitude(float64(buf.ReadInt()) / 10000000.0)
		pos.Altitude = float64(buf.ReadShort())
		pos.Course = float64(buf.ReadUnsignedShort())
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
		pos.Valid = buf.ReadUnsignedByte() != 0
		buf.ReadUnsignedByte() // used systems
		buf.ReadUnsignedByte() // cause element id

		for n := buf.ReadUnsignedByte(); n > 0; n-- {
			pos.Set(model.PrefixIo+itoa(buf.ReadUnsignedByte()), buf.ReadUnsignedByte())
		}
		for n := buf.ReadUnsignedByte(); n > 0; n-- {
			pos.Set(model.PrefixIo+itoa(buf.ReadUnsignedByte()), buf.ReadUnsignedShort())
		}
		for n := buf.ReadUnsignedByte(); n > 0; n-- {
			pos.Set(model.PrefixIo+itoa(buf.ReadUnsignedByte()), buf.ReadUnsignedInt())
		}
		for n := buf.ReadUnsignedByte(); n > 0; n-- {
			pos.Set(model.PrefixIo+itoa(buf.ReadUnsignedByte()), buf.ReadLong())
		}
		positions = append(positions, pos)
	}
	return positions
}

func itoa(i int) string { return kit.Format("%d", i) }
