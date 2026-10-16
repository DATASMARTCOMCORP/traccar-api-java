// Package gs100 is the GS100 protocol (Gs100Protocol): a binary login ("GL") and batches
// of BCD-coded records.
package gs100

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server without framing.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gs100",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	header := buf.ReadString(2)
	if header == "GL" {
		buf.Skip(1)
		imei := buf.ReadString(buf.ReadUnsignedByte())
		if ds := m.Session(imei); ds != nil {
			m.Send(kit.NewBuf(kit.NettyBytes("GS100")))
		}
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}
	var positions []*model.Position
	count := buf.ReadUnsignedByte()
	for range count {
		endIndex := buf.ReadUnsignedByte() + buf.ReaderIndex()

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		status := buf.ReadUnsignedMedium()
		pos.Set(model.KeyStatus, status)

		if kit.Check(int64(status), 8+8+7) {
			b := kit.NewDateBuilder(nil)
			b.SetHour(kit.ReadBCD(buf, 2))
			b.SetMinute(kit.ReadBCD(buf, 2))
			b.SetSecond(kit.ReadBCD(buf, 2))
			b.SetDay(kit.ReadBCD(buf, 2))
			b.SetMonth(kit.ReadBCD(buf, 2))
			b.SetYear(kit.ReadBCD(buf, 2))
			pos.SetTime(b.Date())
			pos.Valid = true

			coordinates := buf.ReadSlice(9).HexDump()
			pos.SetLongitude(float64(kit.Int(coordinates[0:3])) + float64(kit.Int(coordinates[3:9]))/10000.0/60)
			pos.SetLatitude(float64(kit.Int(coordinates[10:12])) + float64(kit.Int(coordinates[12:18]))/10000.0/60)
			flags := kit.IntRadix(coordinates[9:10], 16)
			if !kit.Check(int64(flags), 3) {
				pos.SetLongitude(-pos.Longitude)
			}
			if !kit.Check(int64(flags), 2) {
				pos.SetLatitude(-pos.Latitude)
			}

			other := buf.ReadSlice(4).HexDump()
			pos.Speed = units.KnotsFromKph(float64(kit.Int(other[0:5])) / 100.0)
			pos.Course = float64(kit.Int(other[5:8]))
		} else {
			m.LastLocation(pos, nil)
		}

		positions = append(positions, pos)
		buf.SetReaderIndex(endIndex)
	}

	response := kit.Empty()
	response.WriteASCII("GS100")
	response.WriteByte8(count)
	m.Send(response)

	if len(positions) == 0 {
		return nil
	}
	return positions
}
