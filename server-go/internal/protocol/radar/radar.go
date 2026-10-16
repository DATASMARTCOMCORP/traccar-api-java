// Package radar is the Radar protocol (RadarProtocol): binary tracking records over TCP.
package radar

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "radar",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 12, 2, -14, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const msgTracking = 0x4C

// bitSet is BitSet.valueOf(bytes): bit i is bit i%8 of byte i/8.
type bitSet []byte

func (b bitSet) get(i int) bool { return i/8 < len(b) && b[i/8]>>(uint(i)%8)&1 != 0 }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.ReadUnsignedByte() // product id
	buf.ReadUnsignedByte() // product version
	ds := m.Session(strconv.FormatInt(buf.ReadUnsignedInt(), 10))
	if ds == nil {
		return nil
	}
	buf.ReadUnsignedByte() // index
	buf.ReadUnsignedInt()  // timestamp
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length
	if typ != msgTracking {
		return nil
	}

	buf.ReadUnsignedShort() // memory count
	buf.ReadUnsignedShort() // memory index
	count := buf.ReadUnsignedShort()
	buf.ReadUnsignedShort() // first index

	var positions []*model.Position
	for index := 0; index < count; index++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.Set(model.KeyEvent, buf.ReadUnsignedShort())

		maskLength := buf.ReadUnsignedByte()
		mask := bitSet(buf.GetBytes(buf.ReaderIndex(), maskLength))
		buf.Skip(maskLength)
		buf.ReadUnsignedShort() // length

		if mask.get(0) {
			pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		}
		if mask.get(1) {
			pos.FixTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		}
		if mask.get(2) {
			pos.SetLatitude(float64(buf.ReadInt()) / 360000.0)
		}
		if mask.get(3) {
			pos.SetLongitude(float64(buf.ReadInt()) / 360000.0)
		}
		if mask.get(4) {
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
		}
		if mask.get(5) {
			pos.Course = float64(buf.ReadUnsignedShort()) / 10.0
		}
		if mask.get(6) {
			pos.Altitude = float64(buf.ReadShort())
		}
		if mask.get(7) {
			flags := buf.ReadUnsignedByte()
			pos.Valid = kit.Check(int64(flags), 0)
			pos.Set(model.KeySatellites, kit.FromInt(flags, 4))
		}
		if mask.get(8) {
			flags := buf.ReadUnsignedInt()
			pos.Set(model.KeyIgnition, kit.Check(flags, 0))
			pos.Set(model.KeyCharge, kit.Check(flags, 1))
			pos.Set(model.KeyMotion, kit.Check(flags, 2))
			for i := 0; i < 3; i++ {
				pos.Set(model.PrefixIn+strconv.Itoa(i), kit.Check(flags, 4+i))
			}
		}
		if mask.get(9) {
			flags := int64(buf.ReadUnsignedShort())
			pos.Set(model.KeyBlocked, kit.Check(flags, 0))
			pos.Set(model.PrefixIn+"0", kit.Check(flags, 4))
		}
		for i := 10; i <= 14; i++ {
			if mask.get(i) {
				buf.ReadUnsignedShort() // reserved
			}
		}
		if mask.get(15) {
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
		}
		if mask.get(16) {
			buf.ReadUnsignedInt() // reserved
		}
		for i := 17; i <= 27; i++ {
			if mask.get(i) {
				buf.ReadUnsignedByte() // reserved
			}
		}
		for i := 28; i <= 37; i += 2 {
			if mask.get(i) {
				buf.Skip(12) // reserved
			}
			if mask.get(i + 1) {
				buf.ReadUnsignedByte() // reserved
			}
		}
		skips := []struct{ bit, n int }{
			{38, 6}, {39, 2}, {40, 2}, {41, 2}, {42, 2}, {43, 10}, {44, 2},
			{45, 1}, {46, 1}, {47, 1}, {48, 1}, {49, 1}, {50, 2}, {51, 4}, {52, 4},
		}
		for _, s := range skips {
			if mask.get(s.bit) {
				buf.Skip(s.n) // driver id, hardware problems, acceleration, operator, power, tilt, hours
			}
		}
		if !pos.DeviceTime.IsZero() && !pos.FixTime.IsZero() {
			positions = append(positions, pos)
		}
	}
	if len(positions) == 0 {
		return nil
	}
	return positions
}
