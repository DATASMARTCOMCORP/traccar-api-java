// Package dsf22 is the DSF22 protocol (Dsf22Protocol): binary batches of fixed-size
// records, over TCP and UDP.
package dsf22

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP (framed) and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "dsf22",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

// Framer is Dsf22FrameDecoder: the record count at offset 4 gives the length.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 21 {
			return nil
		}
		count := buf.GetUnsignedByte(buf.ReaderIndex() + 4)
		length := 2 + 2 + 1 + count*(4+4+4+1+2+1)
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2) // header

	ds := m.Session(strconv.Itoa(buf.ReadUnsignedShortLE()))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	count := buf.ReadUnsignedByte()
	for i := 0; i < count; i++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Valid = true
		pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

		pos.Set(model.KeyFuel, float64(buf.ReadUnsignedShortLE())/1000.0)

		status := int64(buf.ReadUnsignedByte())
		pos.Set(model.KeyIgnition, kit.Check(status, 0))
		pos.Set(model.PrefixIn+"1", kit.Check(status, 1))
		pos.Set(model.PrefixOut+"1", kit.Check(status, 4))
		if kit.Check(status, 6) {
			pos.AddAlarm(model.AlarmJamming)
		}
		pos.Set(model.KeyStatus, int(status))

		positions = append(positions, pos)
	}

	m.Send(kit.NewBuf([]byte{0x01}))
	return positions
}
