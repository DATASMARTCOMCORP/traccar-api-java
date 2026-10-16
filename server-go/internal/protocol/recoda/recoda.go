// Package recoda is the Recoda protocol (RecodaProtocol): little-endian binary messages
// with a type and a length.
package recoda

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "recoda",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 4, 4, -8, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgHeartbeat              = 0x00001001
	msgSignalLinkRegistration = 0x20001001
	msgGPSData                = 0x20001011
)

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	typ := buf.ReadIntLE()
	buf.ReadUnsignedIntLE() // length

	if typ != msgHeartbeat {
		buf.ReadUnsignedShortLE() // version
		buf.ReadUnsignedShortLE() // index
	}

	switch typ {
	case msgSignalLinkRegistration:
		m.Session(buf.ReadString(12))
	case msgGPSData:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(time.UnixMilli(buf.ReadLongLE()))

		flags := int64(buf.ReadUnsignedByte())
		if kit.Check(flags, 0) {
			buf.ReadUnsignedShortLE() // declination

			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))

			pos.SetLongitude(float64(buf.ReadUnsignedByte()) + float64(buf.ReadUnsignedByte())/60.0)
			pos.SetLatitude(float64(buf.ReadUnsignedByte()) + float64(buf.ReadUnsignedByte())/60.0)

			pos.SetLongitude(pos.Longitude + float64(buf.ReadUnsignedIntLE())/3600.0)
			pos.SetLatitude(pos.Latitude + float64(buf.ReadUnsignedIntLE())/3600.0)

			status := int64(buf.ReadUnsignedByte())
			pos.Valid = kit.Check(status, 0)
			if kit.Check(status, 1) {
				pos.SetLongitude(-pos.Longitude)
			}
			if !kit.Check(status, 2) {
				pos.SetLatitude(-pos.Latitude)
			}
		} else {
			m.LastLocation(pos, nil)
		}
		return pos
	}
	return nil
}
