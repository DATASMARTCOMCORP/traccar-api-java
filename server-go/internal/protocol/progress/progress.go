// Package progress is the Progress protocol (ProgressProtocol): little-endian binary
// messages with a type and a length; the decoder asks for archived records it missed.
package progress

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "progress",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			framer := ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, 4, 0)
			framer.LittleEndian = true
			return ingest.Pipeline{Framer: framer, Decoder: &Decoder{}}
		}}},
	}
}

const (
	msgIdent     = 1
	msgIdentFull = 2
	msgPoint     = 10
	msgLogSync   = 100
	msgLogmsg    = 101
	msgAlarm     = 200
)

// Decoder is ProgressProtocolDecoder: the archive indexes of the connection.
type Decoder struct {
	lastIndex int64
	newIndex  int64
}

// send is channel.writeAndFlush without Java's null check: with no channel (the Java
// tests) it throws a NullPointerException, as Java does.
func send(m *ingest.Message, data *kit.Buf) {
	if !m.HasChannel() {
		kit.Throw("NullPointerException: no channel")
	}
	m.Send(data)
}

func (d *Decoder) requestArchive(m *ingest.Message) {
	if d.lastIndex == 0 {
		d.lastIndex = d.newIndex
	} else if d.newIndex > d.lastIndex {
		request := kit.Empty()
		request.WriteShortLE(msgLogSync)
		request.WriteShortLE(4)
		request.WriteIntLE(d.lastIndex)
		request.WriteIntLE(0)
		send(m, request)
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	kind := buf.ReadUnsignedShortLE()
	buf.ReadUnsignedShortLE() // length

	switch kind {
	case msgIdent, msgIdentFull:
		buf.ReadUnsignedIntLE() // id
		buf.Skip(buf.ReadUnsignedShortLE())
		buf.Skip(buf.ReadUnsignedShortLE())
		imei := buf.ReadString(buf.ReadUnsignedShortLE())
		m.Session(imei)

	case msgPoint, msgAlarm, msgLogmsg:
		ds := m.Session()
		if ds == nil {
			return nil
		}

		positions := []*model.Position{}

		recordCount := 1
		if kind == msgLogmsg {
			recordCount = buf.ReadUnsignedShortLE()
		}

		for range recordCount {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			if kind == msgLogmsg {
				pos.Set(model.KeyArchive, true)
				if buf.ReadUnsignedShortLE() == msgAlarm {
					pos.AddAlarm(model.AlarmGeneral)
				}
				if buf.ReadUnsignedShortLE() > buf.ReadableBytes() {
					d.lastIndex++
					break // workaround for device bug
				}
				d.lastIndex = buf.ReadUnsignedIntLE()
				pos.Set(model.KeyIndex, d.lastIndex)
			} else {
				d.newIndex = buf.ReadUnsignedIntLE()
			}

			pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
			pos.SetLatitude(float64(buf.ReadIntLE()) * 180.0 / 0x7FFFFFFF)
			pos.SetLongitude(float64(buf.ReadIntLE()) * 180.0 / 0x7FFFFFFF)
			pos.Speed = float64(buf.ReadUnsignedIntLE()) / 100.0
			pos.Course = float64(buf.ReadUnsignedShortLE()) / 100.0
			pos.Altitude = float64(buf.ReadUnsignedShortLE()) / 100.0

			satellites := buf.ReadUnsignedByte()
			pos.Valid = satellites >= 3
			pos.Set(model.KeySatellites, satellites)

			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

			extraFlags := buf.ReadLongLE()

			if kit.Check(extraFlags, 0) {
				count := buf.ReadUnsignedShortLE()
				for i := 1; i <= count; i++ {
					pos.Set(model.PrefixAdc+strconv.Itoa(i), buf.ReadUnsignedShortLE())
				}
			}

			if kit.Check(extraFlags, 1) {
				size := buf.ReadUnsignedShortLE()
				pos.Set("can", buf.GetString(buf.ReaderIndex(), size))
				buf.Skip(size)
			}

			if kit.Check(extraFlags, 2) {
				pos.Set("passenger", buf.ReadSlice(buf.ReadUnsignedShortLE()).HexDump())
			}

			if kind == msgAlarm {
				pos.AddAlarm(model.AlarmGeneral)
				send(m, kit.NewBuf([]byte{0xC9, 0, 0, 0, 0, 0, 0, 0}))
			}

			buf.ReadUnsignedIntLE() // crc

			positions = append(positions, pos)
		}

		d.requestArchive(m)

		return positions
	}

	return nil
}
