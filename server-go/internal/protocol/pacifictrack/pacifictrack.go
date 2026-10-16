// Package pacifictrack is the PacificTrack protocol (PacificTrackProtocol): binary UDP
// frames of segments with variable-length ("bit-extended") ids and lengths.
package pacifictrack

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "pacifictrack",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// readBitExt reads a number in 7-bit groups, the last one with the high bit set.
func readBitExt(buf *kit.Buf) int {
	var result int32
	for buf.IsReadable() {
		b := buf.ReadUnsignedByte()
		result <<= 7
		result += int32(kit.ToInt(b, 7))
		if kit.Check(int64(b), 7) {
			break
		}
	}
	return int(result)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadSignedByte() // frame start
	readBitExt(buf)      // frame control
	readBitExt(buf)      // frame length

	var ds *ingest.DeviceSession
	pos := m.NewPosition()

	for buf.IsReadable() {
		segmentID := readBitExt(buf)
		segmentEnd := readBitExt(buf) + buf.ReaderIndex()

		switch segmentID {
		case 0x01:
			pos.Set(model.KeyEvent, readBitExt(buf))
		case 0x10:
			pos.Valid = kit.Check(int64(buf.ReadUnsignedByte()), 4)
			date := buf.ReadUnsignedByte()
			builder := kit.NewDateBuilder(nil).
				SetDate(2020+kit.FromInt(date, 4), kit.ToInt(date, 4), buf.ReadUnsignedByte()).
				SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			pos.SetTime(builder.Date())
			pos.SetLatitude(float64(buf.ReadUnsignedInt())/1000000.0 - 90.0)
			pos.SetLongitude(float64(buf.ReadUnsignedInt())/1000000.0 - 180.0)
			speedAndCourse := buf.ReadUnsignedMedium()
			pos.Course = float64(kit.FromInt(speedAndCourse, 12))
			pos.Speed = units.KnotsFromKph(float64(kit.ToInt(speedAndCourse, 12)) / 10.0)
			pos.Set(model.KeyIndex, buf.ReadUnsignedShort())
		case 0x20:
			voltage := buf.ReadUnsignedMedium()
			pos.Set(model.KeyBattery, float64(kit.BetweenInt(voltage, 0, 12))/100.0)
			pos.Set(model.KeyPower, float64(kit.BetweenInt(voltage, 12, 24))/100.0)
		case 0x92:
			for buf.ReaderIndex() < segmentEnd {
				decodeField(pos, buf)
			}
		case 0x100:
			imei := kit.Substring(buf.ReadSlice(8).HexDump(), 0, 15)
			ds = m.Session(imei)
		default:
			buf.SetReaderIndex(segmentEnd)
		}
	}

	if ds == nil {
		ds = m.Session()
	}
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID
	if pos.FixTime.IsZero() {
		m.LastLocation(pos, nil)
	}
	return pos
}

// decodeField decodes one field of a 0x92 (engine data) segment.
func decodeField(pos *model.Position, buf *kit.Buf) {
	field := buf.ReadUnsignedByte()
	fieldPrefix := kit.FromInt(field, 5)
	switch {
	case fieldPrefix < 0b100:
		switch kit.BetweenInt(field, 2, 5) {
		case 0b000:
			pos.Set("bus", kit.ToInt(field, 2))
		case 0b001:
			pos.Set("currentGear", kit.ToInt(field, 2))
		}
	case fieldPrefix < 0b101:
		switch kit.ToInt(field, 5) {
		case 0b00000:
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())
		case 0b00001:
			pos.Set(model.KeyRpm, buf.ReadUnsignedByte()*32)
		case 0b00011:
			pos.Set("oilPressure", buf.ReadUnsignedByte()*4)
		case 0b00100:
			pos.Set("oilLevel", float64(buf.ReadUnsignedByte())*0.4)
		case 0b00101:
			pos.Set("oilTemp", buf.ReadUnsignedByte()-40)
		case 0b00110:
			pos.Set("coolantLevel", float64(buf.ReadUnsignedByte())*0.4)
		case 0b00111:
			pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedByte()-40)
		case 0b01000:
			pos.Set(model.KeyFuel, float64(buf.ReadUnsignedByte())*0.4)
		case 0b01001:
			pos.Set("defLevel", float64(buf.ReadUnsignedByte())*0.4)
		case 0b01010:
			pos.Set(model.KeyEngineLoad, buf.ReadUnsignedByte())
		case 0b01011:
			pos.Set("barometer", float64(buf.ReadUnsignedByte())*0.5)
		case 0b01100:
			pos.Set("intakeManifoldTemp", buf.ReadUnsignedByte()-40)
		case 0b01101:
			pos.Set("fuelTankTemp", buf.ReadUnsignedByte()-40)
		case 0b01110:
			pos.Set("intercoolerTemp", buf.ReadUnsignedByte()-40)
		case 0b01111:
			pos.Set("turboOilTemp", buf.ReadUnsignedByte()-40)
		case 0b10000:
			pos.Set("transOilTemp", buf.ReadUnsignedByte()-40)
		default:
			buf.ReadUnsignedByte()
		}
	case fieldPrefix < 0b110:
		switch kit.ToInt(field, 5) {
		case 0b00010:
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())/512.0)
		case 0b00011:
			pos.Set(model.PrefixTemp+"1", float64(buf.ReadUnsignedShort())*0.03125-273)
		default:
			buf.ReadUnsignedShort()
		}
	case fieldPrefix < 0b111:
		switch kit.ToInt(field, 5) {
		case 0b00000:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
		case 0b00001:
			pos.Set(model.KeyHours, buf.ReadUnsignedInt()*180)
		case 0b00010:
			pos.Set("idleHours", buf.ReadUnsignedInt()*180)
		case 0b00100:
			pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())*0.5)
		case 0b00101:
			pos.Set("fuelUsedIdle", float64(buf.ReadUnsignedInt())*0.5)
		default:
			buf.ReadUnsignedInt()
		}
	default:
		buf.Skip(buf.ReadUnsignedByte())
	}
}
