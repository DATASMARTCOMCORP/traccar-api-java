// Package vt200 is the VT200 protocol (Vt200Protocol): escaped binary frames ending with
// ')' over TCP.
package vt200

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
		Name: "vt200",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is Vt200FrameDecoder: frames end with ')'; '=' escapes the next byte (XOR '=').
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ')') + 1
		if endIndex > 0 {
			frame := kit.Empty()
			for buf.ReaderIndex() < endIndex {
				b := int(buf.ReadSignedByte())
				if b == '=' {
					frame.WriteByte8(int(buf.ReadSignedByte()) ^ '=')
				} else {
					frame.WriteByte8(b)
				}
			}
			return frame
		}
		return nil
	}}
}

func decodeCoordinate(value int) float64 {
	degrees := value / 1000000
	minutes := value % 1000000
	return float64(degrees) + float64(minutes)/10000.0/60
}

func decodeDate(buf *kit.Buf) time.Time {
	b := kit.NewDateBuilder(nil)
	b.SetDateReverse(kit.ReadBCD(buf, 2), kit.ReadBCD(buf, 2), kit.ReadBCD(buf, 2))
	b.SetTime(kit.ReadBCD(buf, 2), kit.ReadBCD(buf, 2), kit.ReadBCD(buf, 2))
	return b.Date()
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(1) // header

	id := buf.ReadSlice(6).HexDump()
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	typ := buf.ReadUnsignedShort()
	buf.ReadUnsignedShort() // length

	switch typ {
	case 0x2086, 0x2084, 0x2082, 0x3089:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		if typ == 0x3089 {
			pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() == 1)
		} else {
			buf.ReadUnsignedByte() // data type
		}

		buf.ReadUnsignedShort() // trip id

		pos.SetTime(decodeDate(buf))

		if buf.ReadableBytes() > 2 {
			pos.SetLatitude(decodeCoordinate(kit.ReadBCD(buf, 8)))
			pos.SetLongitude(decodeCoordinate(kit.ReadBCD(buf, 9)))

			flags := int64(buf.ReadUnsignedByte())
			pos.Valid = kit.Check(flags, 0)
			if !kit.Check(flags, 1) {
				pos.SetLatitude(-pos.Latitude)
			}
			if !kit.Check(flags, 2) {
				pos.SetLongitude(-pos.Longitude)
			}
		}

		if typ != 0x3089 {
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
			pos.Course = float64(buf.ReadUnsignedByte() * 2)

			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)
			pos.Set(model.KeyStatus, buf.ReadUnsignedInt())
		}

		return pos

	case 0x3088:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		buf.ReadUnsignedShort() // trip id
		buf.Skip(8)             // imei
		buf.Skip(8)             // imsi

		pos.Set("tripStart", decodeDate(buf).UnixMilli())
		pos.Set("tripEnd", decodeDate(buf).UnixMilli())
		pos.Set(model.KeyDrivingTime, buf.ReadUnsignedShort())

		pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedInt())
		pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedInt())

		pos.Set("maxSpeed", units.KnotsFromKph(float64(buf.ReadUnsignedByte())))
		pos.Set("maxRpm", buf.ReadUnsignedShort())
		pos.Set("maxTemp", buf.ReadUnsignedByte()-40)
		pos.Set("hardAccelerationCount", buf.ReadUnsignedByte())
		pos.Set("hardBrakingCount", buf.ReadUnsignedByte())

		for _, speedType := range []string{"over", "high", "normal", "low"} {
			pos.Set(speedType+"SpeedTime", buf.ReadUnsignedShort())
			pos.Set(speedType+"SpeedDistance", buf.ReadUnsignedInt())
			pos.Set(speedType+"SpeedFuel", buf.ReadUnsignedInt())
		}

		pos.Set("idleTime", buf.ReadUnsignedShort())
		pos.Set("idleFuel", buf.ReadUnsignedInt())

		pos.Set("hardCorneringCount", buf.ReadUnsignedByte())
		pos.Set("overspeedCount", buf.ReadUnsignedByte())
		pos.Set("overheatCount", buf.ReadUnsignedShort())
		pos.Set("laneChangeCount", buf.ReadUnsignedByte())
		pos.Set("emergencyRefueling", buf.ReadUnsignedByte())

		return pos
	}

	return nil
}
