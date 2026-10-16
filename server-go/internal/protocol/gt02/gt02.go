// Package gt02 is the GT02 protocol (Gt02Protocol): binary messages with a one-byte length
// after a two-byte header.
package gt02

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gt02",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 1, 2, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgData      = 0x10
	msgHeartbeat = 0x1A
	msgResponse  = 0x1C
)

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2)          // header
	buf.ReadSignedByte() // size

	pos := m.NewPosition()

	// Zero for location messages
	power := buf.ReadUnsignedByte()
	gsm := buf.ReadUnsignedByte()

	imei := buf.ReadSlice(8).HexDump()[1:]
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyIndex, buf.ReadUnsignedShort())

	switch buf.ReadUnsignedByte() {
	case msgHeartbeat:
		m.LastLocation(pos, nil)
		pos.Set(model.KeyPower, power)
		pos.Set(model.KeyRssi, gsm)
		m.Send(kit.NewBuf([]byte{0x54, 0x68, 0x1A, 0x0D, 0x0A}))

	case msgData:
		b := kit.NewDateBuilder(nil)
		b.SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		b.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		pos.SetTime(b.Date())

		latitude := float64(buf.ReadUnsignedInt()) / (60.0 * 30000.0)
		longitude := float64(buf.ReadUnsignedInt()) / (60.0 * 30000.0)

		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Course = float64(buf.ReadUnsignedShort())

		buf.Skip(3) // reserved

		flags := buf.ReadUnsignedInt()
		pos.Valid = kit.Check(flags, 0)
		if !kit.Check(flags, 1) {
			latitude = -latitude
		}
		if !kit.Check(flags, 2) {
			longitude = -longitude
		}
		pos.SetLatitude(latitude)
		pos.SetLongitude(longitude)

	case msgResponse:
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, kit.Latin1(buf.ReadSlice(buf.ReadUnsignedByte()).Bytes()))

	default:
		return nil
	}
	return pos
}
