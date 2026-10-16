// Package satsol is the Satsol protocol (SatsolProtocol): little-endian binary packets of
// location records.
package satsol

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "satsol",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLengthLarge, 8, 2, 0, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedShortLE() // checksum
	buf.ReadUnsignedShortLE() // preamble
	id := buf.ReadUnsignedIntLE()
	buf.ReadUnsignedShortLE() // length

	ds := m.Session(strconv.FormatInt(id, 10))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}

	for buf.IsReadable() {
		buf.ReadUnsignedShortLE() // checksum
		buf.ReadUnsignedShortLE() // checksum
		buf.ReadUnsignedShortLE() // type
		length := buf.ReadUnsignedShortLE()

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		pos.SetLatitude(float64(buf.ReadUnsignedIntLE()) / 1000000.0)
		pos.SetLongitude(float64(buf.ReadUnsignedIntLE()) / 1000000.0)
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()) / 100.0)
		pos.Altitude = float64(buf.ReadShortLE())
		pos.Course = float64(buf.ReadUnsignedShortLE())
		pos.Valid = buf.ReadUnsignedByte() > 0

		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())

		if kit.Check(int64(buf.ReadUnsignedByte()), 0) {
			pos.Set(model.KeyArchive, true)
		}

		positions = append(positions, pos)

		buf.Skip(length)
	}

	response := kit.Empty()
	response.WriteShortLE(0)
	response.WriteShortLE(0x4CBF) // preamble
	response.WriteIntLE(id)
	response.WriteShortLE(0)
	response.SetShortLE(0, kit.CRC16(kit.CRC16_CCITT_FALSE, response.Bytes()[2:response.ReadableBytes()]))
	m.Send(response)

	return positions
}
