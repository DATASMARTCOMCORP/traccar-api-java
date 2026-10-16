// Package vnet is the Vnet protocol (VnetProtocol): little-endian binary frames with BCD
// fields over TCP.
package vnet

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "vnet",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer: &ingest.LengthFramer{
					MaxLength: ingest.MaxFrameLengthLarge, FieldOffset: 4, FieldLength: 2,
					Adjustment: 12, LittleEndian: true,
				},
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgLogin = 0x0000
	msgLBS   = 0x32
	msgGPS   = 0x33
)

// readBCD is BcdUtil.readInteger, wrapped to a Java int.
func readBCD(buf *kit.Buf, digits int) int { return int(int32(kit.ReadBCD(buf, digits))) }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	typ := kit.ToInt(buf.ReadUnsignedShortLE(), 15)
	buf.ReadUnsignedShortLE() // length

	b := kit.NewDateBuilder(nil)
	b.SetDateReverse(readBCD(buf, 2), readBCD(buf, 2), readBCD(buf, 2))
	b.SetTime(readBCD(buf, 2), readBCD(buf, 2), readBCD(buf, 2))

	switch typ {
	case msgLogin:
		imei := kit.Substring(buf.ReadSlice(8).HexDump(), 0, 15)
		m.Session(imei)
		m.Send(kit.NewBuf(append([]byte(nil), buf.Array()[:buf.WriterIndex()]...)))

	case msgGPS:
		ds := m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(b.Date())

		value := readBCD(buf, 8)
		degrees := value / 1000000
		lat := float64(degrees) + float64(value%1000000)/10000.0/60

		value = readBCD(buf, 10)
		degrees = value / 10000000
		lon := float64(degrees) + float64(value%10000000)/100000.0/60

		flags := int64(buf.ReadUnsignedByte())
		pos.Valid = kit.Check(flags, 0)
		if !kit.Check(flags, 1) {
			lat = -lat
		}
		pos.SetLatitude(lat)
		if !kit.Check(flags, 2) {
			lon = -lon
		}
		pos.SetLongitude(lon)

		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
		pos.Course = float64(buf.ReadUnsignedByte() * 2)

		return pos
	}

	return nil
}
