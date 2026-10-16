// Package pt215 is the PT215 protocol (Pt215Protocol): binary "XX" messages with a
// length byte.
package pt215

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "pt215",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 1, -1, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgLogin       = 0x01
	msgHeartbeat   = 0x08
	msgGPSRealtime = 0x10
	msgGPSOffline  = 0x11
	msgStatus      = 0x13
)

func sendResponse(m *ingest.Message, kind int, content []byte) {
	response := kit.Empty()
	response.WriteByte8('X')
	response.WriteByte8('X')
	response.WriteByte8(1 + len(content))
	response.WriteByte8(kind)
	response.WriteBytes(content)
	response.WriteByte8('\r')
	response.WriteByte8('\n')
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2)            // header
	buf.ReadUnsignedByte() // length
	kind := buf.ReadUnsignedByte()

	switch kind {
	case msgLogin:
		m.Session(kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1))
		sendResponse(m, kind, nil)

	case msgGPSOffline, msgGPSRealtime:
		ds := m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		sendResponse(m, kind, buf.GetBytes(buf.ReaderIndex(), 6))

		date := kit.NewDateBuilder(nil).
			SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		pos.SetTime(date.Date())

		latitude := float64(buf.ReadUnsignedInt()) / 60.0 / 30000.0
		longitude := float64(buf.ReadUnsignedInt()) / 60.0 / 30000.0

		flags := buf.ReadUnsignedShort()
		pos.Course = float64(kit.ToInt(flags, 10))
		pos.Valid = kit.Check(int64(flags), 12)

		if !kit.Check(int64(flags), 10) {
			latitude = -latitude
		}
		if kit.Check(int64(flags), 11) {
			longitude = -longitude
		}

		pos.SetLatitude(latitude)
		pos.SetLongitude(longitude)

		return pos
	}

	return nil
}
