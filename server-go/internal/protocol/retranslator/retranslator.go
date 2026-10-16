// Package retranslator is the Wialon Retranslator protocol (RetranslatorProtocol): binary
// packets of named blocks, each with a little-endian length prefix.
package retranslator

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
		Name: "retranslator",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is RetranslatorFrameDecoder: a 4-byte little-endian length, then the packet.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		length := 4 + int(buf.GetIntLE(buf.ReaderIndex()))
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

// readCString reads a zero-terminated string and its terminator.
func readCString(buf *kit.Buf) string {
	length := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0) - buf.ReaderIndex()
	s := buf.ReadString(length)
	buf.ReadSignedByte()
	return s
}

func decode(m *ingest.Message, msg any) any {
	m.Send(kit.NewBuf([]byte{0x11}))

	buf := msg.(*kit.Buf)
	buf.ReadUnsignedInt() // length

	id := readCString(buf)
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

	buf.ReadUnsignedInt() // bit flags

	for buf.IsReadable() {
		buf.ReadUnsignedShort() // block type
		blockEnd := int(buf.ReadInt()) + buf.ReaderIndex()
		buf.ReadUnsignedByte() // security attribute
		dataType := buf.ReadUnsignedByte()

		name := readCString(buf)

		if name == "posinfo" {
			pos.Valid = true
			pos.SetLongitude(buf.ReadDoubleLE())
			pos.SetLatitude(buf.ReadDoubleLE())
			pos.Altitude = buf.ReadDoubleLE()
			pos.Speed = units.KnotsFromKph(float64(buf.ReadShort()))
			pos.Course = float64(buf.ReadShort())
			pos.Set(model.KeySatellites, int(buf.ReadSignedByte()))
		} else {
			switch dataType {
			case 1:
				pos.Set(name, readCString(buf))
			case 3:
				pos.Set(name, int(buf.ReadInt()))
			case 4:
				pos.Set(name, buf.ReadDoubleLE())
			case 5:
				pos.Set(name, buf.ReadLong())
			}
		}

		buf.SetReaderIndex(blockEnd)
	}

	if pos.Latitude == 0 && pos.Longitude == 0 {
		t := pos.DeviceTime.Time
		m.LastLocation(pos, &t)
	}

	return pos
}
