// Package gl601 is the GL601 protocol (Gl601Protocol): binary records over TCP, framed by
// a 2-byte length at offset 2.
package gl601

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
		Name: "gl601",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, -4, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// readVariableInt reads a byte, or a short (its high bit kept, as Java) when the high bit
// is set.
func readVariableInt(buf *kit.Buf) int {
	if buf.GetUnsignedByte(buf.ReaderIndex())&0x80 != 0 {
		return buf.ReadUnsignedShort()
	}
	return buf.ReadUnsignedByte()
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte()  // header
	buf.ReadUnsignedByte()  // reserved
	buf.ReadUnsignedShort() // length

	flags := buf.ReadUnsignedByte()
	count := 1
	if flags&0x80 != 0 {
		count = buf.ReadUnsignedShort()
	}

	imei := buf.ReadSlice(8).HexDump()[1:]
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	buf.ReadUnsignedShort() // device type
	buf.ReadUnsignedShort() // protocol version
	buf.ReadUnsignedByte()  // custom version

	buf.Skip(buf.ReadUnsignedByte()) // reserved

	positions := []*model.Position{}
	for range count {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		recordEnd := buf.ReaderIndex() + readVariableInt(buf)

		pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

		buf.ReadUnsignedShort() // record index
		buf.ReadUnsignedByte()  // record id
		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())

		for buf.ReaderIndex() < recordEnd {
			dataID := readVariableInt(buf)
			dataEnd := readVariableInt(buf) + buf.ReaderIndex()

			switch dataID {
			case 81, 82:
				state := buf.ReadUnsignedByte()
				pos.Valid = kit.BetweenInt(state, 2, 4) == 0b10
				pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
				pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
				pos.FixTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)
				if dataID == 82 {
					pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
					pos.Course = float64(buf.ReadUnsignedShort())
					pos.Altitude = float64(buf.ReadMedium()) / 10.0
					pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
				}
			case 97:
				buf.ReadUnsignedByte() // battery status
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
				buf.ReadUnsignedByte() // charging
			}

			buf.SetReaderIndex(dataEnd)
		}

		if pos.FixTime.Time.IsZero() {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}

		positions = append(positions, pos)
	}

	if len(positions) == 0 {
		return nil
	}
	return positions
}
