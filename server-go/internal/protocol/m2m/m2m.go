// Package m2m is the M2M protocol (M2mProtocol): fixed 23-byte binary frames, offset by
// 0x20; the first frame of a connection carries the IMEI.
package m2m

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "m2m",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.FixedFramer{Length: 23},
				Decoder: &Decoder{},
			}
		}}},
	}
}

// Decoder is M2mProtocolDecoder: the connection's first frame identifies the device.
type Decoder struct {
	identified bool // !firstPacket
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	// Remove offset (absolute indexes, as the Java decoder)
	for i := 0; i < buf.ReadableBytes(); i++ {
		if b := int(buf.GetByte(i)); b != 0x0b {
			buf.SetByte(i, b-0x20)
		}
	}

	if !d.identified {
		d.identified = true
		imei := kit.SubstringFrom(kit.ReadDecimalDigits(buf, 8), 1)
		m.Session(imei)
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).
		SetDay(buf.ReadUnsignedByte() & 0x3f).
		SetMonth(buf.ReadUnsignedByte() & 0x3f).
		SetYear(buf.ReadUnsignedByte()).
		SetHour(buf.ReadUnsignedByte() & 0x3f).
		SetMinute(buf.ReadUnsignedByte() & 0x7f).
		SetSecond(buf.ReadUnsignedByte() & 0x7f)
	pos.SetTime(date.Date())

	degrees := buf.ReadUnsignedByte()
	latitude := float64(buf.ReadUnsignedByte())
	latitude += float64(buf.ReadUnsignedByte()) / 100.0
	latitude += float64(buf.ReadUnsignedByte()) / 10000.0
	latitude /= 60
	latitude += float64(degrees)

	b := buf.ReadUnsignedByte()
	degrees = (b&0x7f)*100 + buf.ReadUnsignedByte()
	longitude := float64(buf.ReadUnsignedByte())
	longitude += float64(buf.ReadUnsignedByte()) / 100.0
	longitude += float64(buf.ReadUnsignedByte()) / 10000.0
	longitude /= 60
	longitude += float64(degrees)

	if b&0x80 != 0 {
		longitude = -longitude
	}
	if b&0x40 != 0 {
		latitude = -latitude
	}

	pos.Valid = true
	pos.SetLatitude(latitude)
	pos.SetLongitude(longitude)
	pos.Speed = float64(buf.ReadUnsignedByte())

	satellites := buf.ReadUnsignedByte()
	if satellites == 0 {
		return nil // cell information
	}
	pos.Set(model.KeySatellites, satellites)
	return pos
}
