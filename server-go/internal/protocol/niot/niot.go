// Package niot is the NIOT protocol (NiotProtocol): "XX"-headed binary packets with a
// length field, each acknowledged.
package niot

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "niot",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 0, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgResponse     = 0x21
	msgPositionData = 0x80
)

func sendResponse(m *ingest.Message, t, checksum int) {
	response := kit.Empty()
	response.WriteShort(0x5858) // header
	response.WriteByte8(msgResponse)
	response.WriteShort(5) // length
	response.WriteByte8(checksum)
	response.WriteByte8(t)
	response.WriteByte8(0) // subtype
	response.WriteByte8(kit.XOR(response.Bytes()[2:]))
	response.WriteByte8(0x0D)
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	t := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length

	imei := buf.ReadSlice(8).HexDump()[1:]

	sendResponse(m, t, int(buf.GetByte(buf.WriterIndex()-2)))

	if t != msgPositionData {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	bcd := func(digits int) int { return kit.ReadBCD(buf, digits) }
	b := kit.NewDateBuilder(nil)
	b.SetYear(bcd(2))
	b.SetMonth(bcd(2))
	b.SetDay(bcd(2))
	b.SetHour(bcd(2))
	b.SetMinute(bcd(2))
	b.SetSecond(bcd(2))
	pos.SetTime(b.Date())

	pos.SetLatitude(float64(kit.ReadSignedMagnitudeInt(buf)) / 1800000.0)
	pos.SetLongitude(float64(kit.ReadSignedMagnitudeInt(buf)) / 1800000.0)
	bcd(4) // reserved
	pos.Course = float64(bcd(4))

	statusX := buf.ReadUnsignedByte()
	pos.Valid = kit.Check(int64(statusX), 7)
	switch kit.BetweenInt(statusX, 3, 5) {
	case 0b10:
		pos.AddAlarm(model.AlarmPowerCut)
	case 0b01:
		pos.AddAlarm(model.AlarmLowPower)
	}

	pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())

	statusA := buf.ReadUnsignedByte()
	pos.Set(model.KeyIgnition, !kit.Check(int64(statusA), 7))
	if !kit.Check(int64(statusA), 6) {
		pos.AddAlarm(model.AlarmOverspeed)
	}

	buf.ReadUnsignedByte() // statusB
	buf.ReadUnsignedByte() // statusC
	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/10.0)
	pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
	buf.ReadUnsignedByte() // speed limit
	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	buf.ReadUnsignedByte() // sensor speed
	buf.ReadUnsignedByte() // reserved
	buf.ReadUnsignedByte() // reserved

	for buf.ReadableBytes() > 4 {
		extendedLength := buf.ReadUnsignedShort()
		extendedType := buf.ReadUnsignedShort()
		switch extendedType {
		case 0x0001:
			pos.Set(model.KeyIccid, buf.ReadString(20))
		case 0x0002:
			statusD := int64(buf.ReadUnsignedByte())
			if kit.Check(statusD, 5) {
				pos.AddAlarm(model.AlarmRemoving)
			}
			if kit.Check(statusD, 4) {
				pos.AddAlarm(model.AlarmTampering)
			}
			buf.ReadUnsignedByte() // run mode
			buf.ReadUnsignedByte() // reserved
		default:
			buf.Skip(extendedLength - 2)
		}
	}

	return pos
}
