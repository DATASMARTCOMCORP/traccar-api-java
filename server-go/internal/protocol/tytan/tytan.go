// Package tytan is the Tytan protocol (TytanProtocol): binary UDP packets of records.
package tytan

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tytan",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func decodeExtraData(pos *model.Position, buf *kit.Buf, end int) {
	for buf.ReaderIndex() < end {

		typ := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()
		if length == 255 {
			length += buf.ReadUnsignedByte()
		}

		switch typ {
		case 2:
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedMedium())
		case 5:
			pos.Set(model.KeyInput, buf.ReadUnsignedByte())
		case 6:
			n := buf.ReadUnsignedByte() >> 4
			if n < 2 {
				pos.Set(model.PrefixAdc+kit.Format("%d", n), float64(buf.ReadFloat()))
			} else {
				pos.Set("di"+kit.Format("%d", n-2), float64(buf.ReadFloat()))
			}
		case 7:
			alarm := buf.ReadUnsignedByte()
			buf.ReadUnsignedByte()
			if kit.Check(int64(alarm), 5) {
				pos.AddAlarm(model.AlarmGeneral)
			}
		case 8:
			pos.Set("antihijack", buf.ReadUnsignedByte())
		case 9:
			pos.Set("unauthorized", buf.ReadSlice(8).HexDump())
		case 10:
			pos.Set("authorized", buf.ReadSlice(8).HexDump())
		case 24:
			for i := 0; i < length/2; i++ {
				key := model.PrefixTemp + kit.Format("%d", buf.ReadUnsignedByte())
				pos.Set(key, int(buf.ReadSignedByte()))
			}
		case 28:
			pos.Set(model.KeyAxleWeight, buf.ReadUnsignedShort())
			buf.ReadUnsignedByte()
		case 90:
			pos.Set(model.KeyPower, float64(buf.ReadFloat()))
		case 101:
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())
		case 102:
			pos.Set(model.KeyRpm, buf.ReadUnsignedByte()*50)
		case 107:
			fuel := buf.ReadUnsignedShort()
			switch fuel >> 14 {
			case 1:
				pos.Set("fuelValue", model.JavaToString(float64(fuel&0x3fff)*0.4)+"%")
			case 2:
				pos.Set("fuelValue", model.JavaToString(float64(fuel&0x3fff)*0.5)+" l")
			case 3:
				pos.Set("fuelValue", model.JavaToString(float64(fuel&0x3fff)*-0.5)+" l")
			}
		case 108:
			pos.Set(model.KeyObdOdometer, buf.ReadUnsignedInt()*5)
		case 150:
			pos.Set(model.KeyDoor, buf.ReadUnsignedByte())
		default:
			buf.Skip(length)
		}
	}
}

// decode is TytanProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte()  // protocol
	buf.ReadUnsignedShort() // length
	index := buf.ReadUnsignedByte() >> 3

	if m.HasChannel() {
		m.Send(kit.NewBuf(kit.NettyBytes("^" + kit.Format("%d", index))))
	}

	ds := m.Session(kit.Format("%d", buf.ReadUnsignedInt()))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}

	for buf.ReadableBytes() > 2 {

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		end := buf.ReaderIndex() + buf.ReadUnsignedByte()

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

		flags := int64(buf.ReadUnsignedByte())
		pos.Set(model.KeySatellites, int(kit.From(flags, 2)))
		pos.Valid = kit.To(flags, 2) > 0

		lat := float64(buf.ReadUnsignedMedium())
		pos.SetLatitude(lat*-180/16777216 + 90)

		lon := float64(buf.ReadUnsignedMedium())
		pos.SetLongitude(lon*360/16777216 - 180)

		flags = int64(buf.ReadUnsignedByte())
		pos.Set(model.KeyIgnition, kit.Check(flags, 0))
		pos.Set(model.KeyRssi, int(kit.Between(flags, 2, 5)))
		pos.Course = float64((kit.From(flags, 5)*45 + 180) % 360)

		if speed := buf.ReadUnsignedByte(); speed < 250 {
			pos.Speed = units.KnotsFromKph(float64(speed))
		}

		decodeExtraData(pos, buf, end)

		positions = append(positions, pos)
	}

	return positions
}
