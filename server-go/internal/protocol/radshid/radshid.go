// Package radshid is the Radshid protocol (RadshidProtocol): little-endian framed binary
// events over TCP.
package radshid

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
		Name: "radshid",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLengthLarge, 0, 4, 0, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func decodeAlarm(event int) string {
	switch event {
	case 0xE2:
		return model.AlarmPowerCut
	case 0xE3:
		return model.AlarmPowerRestored
	case 0xE4, 0xF1, 0xF2:
		return model.AlarmGpsAntennaCut
	case 0xE5:
		return model.AlarmVibration
	case 0xE7, 0xF7:
		return model.AlarmFatigueDriving
	case 0xEE, 0xEF:
		return model.AlarmOverspeed
	case 0xF0:
		return model.AlarmLowBattery
	case 0xF9:
		return model.AlarmJamming
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	payloadLength := int32(buf.ReadUnsignedIntLE())
	packetTag := buf.ReadUnsignedByte()
	serialNumber := buf.ReadUnsignedInt()
	eventCount := buf.ReadUnsignedByte()

	ds := m.Session(strconv.FormatInt(serialNumber, 10))
	if ds == nil {
		return nil
	}
	response := kit.Empty()
	response.WriteIntLE(0x07)
	response.WriteByte8(0x82)
	response.WriteByte8(packetTag)
	response.WriteIntLE(int64(payloadLength))
	response.WriteByte8(0x01)
	m.Send(response)

	var positions []*model.Position
	for i := 0; i < eventCount; i++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		buf.ReadUnsignedByte() // event version
		eventCode := buf.ReadUnsignedByte()
		pos.Set(model.KeyEvent, eventCode)
		pos.AddAlarm(decodeAlarm(eventCode))

		if driverID := buf.ReadUnsignedInt()<<8 | int64(buf.ReadUnsignedByte()); driverID != 0 {
			pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(driverID, 10))
		}
		pos.Set(model.KeyHours, int64(buf.ReadUnsignedShort())*60000)
		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())
		buf.ReadUnsignedByte() // max gps speed
		buf.ReadUnsignedByte() // max sensor speed
		pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		buf.ReadUnsignedInt() // sensor odometer

		ioStatus := buf.ReadUnsignedByte()
		pos.Set(model.KeyCharge, kit.Check(int64(ioStatus), 7))
		pos.Set(model.KeyIgnition, kit.Check(int64(ioStatus), 6))
		pos.Set(model.KeyInput, kit.ToInt(ioStatus, 4))

		pos.Valid = buf.ReadUnsignedByte() != 0x01
		pos.SetLatitude(float64(buf.ReadInt()) / 10000000.0)
		pos.SetLongitude(float64(buf.ReadInt()) / 10000000.0)
		pos.Altitude = float64(buf.ReadShort())
		pos.Course = float64(buf.ReadUnsignedShort())
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Set(model.KeyPdop, float64(buf.ReadUnsignedByte())/10.0)

		extraLength := buf.ReadUnsignedByte()
		extraEnd := buf.ReaderIndex() + extraLength
		if extraLength > 0 && buf.ReadUnsignedByte() == 0x07 {
			pos.Set(model.KeyVersionFw, buf.ReadUnsignedShortLE())
			buf.ReadUnsignedIntLE()   // point counter
			buf.ReadUnsignedIntLE()   // system live time
			buf.ReadUnsignedShortLE() // reset number
			buf.ReadUnsignedShortLE() // flash stored points
			buf.ReadUnsignedShortLE() // sim charge
			buf.ReadUnsignedShortLE() // pause time
			buf.ReadUnsignedShortLE() // off time
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			buf.ReadUnsignedByte() // device status
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte())/10.0)
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			buf.ReadUnsignedByte() // saved pictures
			buf.ReadUnsignedByte() // reset source
			pos.Set(model.PrefixTemp+"1", float64(buf.ReadIntLE())/10.0)
			pos.Set(model.KeyHumidity, float64(buf.ReadIntLE())/10.0)
		}
		buf.SetReaderIndex(extraEnd)
		positions = append(positions, pos)
	}
	if len(positions) == 0 {
		return nil
	}
	return positions
}
