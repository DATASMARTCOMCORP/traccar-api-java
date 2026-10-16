// Package khd is the KHD protocol (KhdProtocol): binary 0x2929 messages with a length
// field, over TCP.
package khd

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "khd",
		DataCommands: protocol.DataCommands("khd"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 0, 0),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Message types.
const (
	msgLogin            = 0xB1
	msgConfirmation     = 0x21
	msgOnDemand         = 0x81
	msgPositionUpload   = 0x80
	msgPositionReupload = 0x8E
	msgAlarm            = 0x82
	msgAdminNumber      = 0x83
	msgSendText         = 0x84
	msgReply            = 0x85
	msgSmsAlarmSwitch   = 0x86
	msgPeripheral       = 0xA3

	msgOnDemandTrack = 0x30
	msgCutOil        = 0x39
	msgResumeOil     = 0x38
	msgCheckVersion  = 0x3D
	msgFactoryReset  = 0xC3
	msgSetOverspeed  = 0x3F
	msgDeleteMileage = 0x66
)

func readIdentifiers(buf *kit.Buf) []string {
	first := buf.Slice(buf.ReaderIndex(), 4).HexDump()
	b1 := buf.ReadUnsignedByte()
	b2 := buf.ReadUnsignedByte() - 0x80
	b3 := buf.ReadUnsignedByte() - 0x80
	b4 := buf.ReadUnsignedByte()
	return []string{first, kit.Format("%02d%02d%02d%02d", b1, b2, b3, b4)}
}

func decodeAlarmStatus(pos *model.Position, status []byte) {
	c := func(i, bit int) bool { return kit.Check(int64(int8(status[i])), bit) }
	switch {
	case c(0, 4):
		pos.AddAlarm(model.AlarmLowPower)
	case c(0, 6):
		pos.AddAlarm(model.AlarmGeofenceExit)
	case c(0, 7):
		pos.AddAlarm(model.AlarmGeofenceEnter)
	case c(1, 0):
		pos.AddAlarm(model.AlarmSos)
	case c(1, 1):
		pos.AddAlarm(model.AlarmOverspeed)
	case c(1, 3):
		pos.AddAlarm(model.AlarmPowerCut)
	case c(1, 6):
		pos.AddAlarm(model.AlarmTow)
	case c(1, 7):
		pos.AddAlarm(model.AlarmDoor)
	case c(2, 2):
		pos.AddAlarm(model.AlarmTemperature)
	case c(2, 4):
		pos.AddAlarm(model.AlarmTampering)
	case c(2, 6):
		pos.AddAlarm(model.AlarmFatigueDriving)
	case c(2, 7):
		pos.AddAlarm(model.AlarmIdle)
	case c(6, 3):
		pos.AddAlarm(model.AlarmVibration)
	case c(6, 4):
		pos.AddAlarm(model.AlarmBraking)
	case c(6, 5):
		pos.AddAlarm(model.AlarmAcceleration)
	case c(6, 6):
		pos.AddAlarm(model.AlarmCornering)
	case c(6, 7):
		pos.AddAlarm(model.AlarmAccident)
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // size

	switch typ {
	case msgLogin, msgAdminNumber, msgSendText, msgSmsAlarmSwitch, msgPositionReupload:
		response := kit.Empty()
		response.WriteByte8(0x29)
		response.WriteByte8(0x29) // header
		response.WriteByte8(msgConfirmation)
		response.WriteShort(5) // size
		response.WriteByte8(int(buf.GetByte(buf.WriterIndex() - 2)))
		response.WriteByte8(typ)
		tenth := 0
		if buf.WriterIndex() > 9 {
			tenth = int(buf.GetByte(9))
		}
		response.WriteByte8(tenth) // 10th byte
		response.WriteByte8(kit.XOR(response.Bytes()))
		response.WriteByte8(0x0D) // ending
		m.Send(response)
	}

	switch typ {
	case msgOnDemand, msgPositionUpload, msgPositionReupload, msgAlarm, msgReply, msgPeripheral:
	default:
		return nil
	}

	pos := m.NewPosition()
	ds := m.Session(readIdentifiers(buf)...)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil).
		SetYear(kit.ReadBCD(buf, 2)).
		SetMonth(kit.ReadBCD(buf, 2)).
		SetDay(kit.ReadBCD(buf, 2)).
		SetHour(kit.ReadBCD(buf, 2)).
		SetMinute(kit.ReadBCD(buf, 2)).
		SetSecond(kit.ReadBCD(buf, 2))
	pos.SetTime(b.Date())

	pos.SetLatitude(kit.ReadBCDCoordinate(buf))
	pos.SetLongitude(kit.ReadBCDCoordinate(buf))
	pos.Speed = units.KnotsFromKph(float64(kit.ReadBCD(buf, 4)))
	pos.Course = float64(kit.ReadBCD(buf, 4))
	pos.Valid = buf.ReadUnsignedByte()&0x80 != 0

	if typ == msgAlarm {
		buf.ReadUnsignedByte() // overloaded state
		buf.ReadUnsignedByte() // logging status
		decodeAlarmStatus(pos, buf.ReadBytes(8))
		return pos
	}

	odometer := buf.ReadUnsignedMedium()
	if kit.ToInt(odometer, 16) > 0 {
		pos.Set(model.KeyOdometer, odometer)
	} else if odometer > 0 {
		pos.Set(model.KeyFuel, kit.FromInt(odometer, 16))
	}

	status := buf.ReadUnsignedInt()
	pos.Set(model.KeyIgnition, !kit.Check(status, 7+3*8))
	pos.Set(model.KeyStatus, status)

	buf.ReadUnsignedShort()
	buf.ReadUnsignedByte()
	buf.ReadUnsignedByte()
	buf.ReadUnsignedByte()
	buf.ReadUnsignedByte()
	buf.ReadUnsignedByte()

	pos.Set(model.KeyResult, strconv.Itoa(buf.ReadUnsignedByte()))

	if typ == msgPeripheral {
		buf.ReadUnsignedShort() // data length

		dataType := buf.ReadUnsignedByte()
		dataLength := buf.ReadUnsignedByte()

		switch dataType {
		case 0x01:
			pos.Set(model.KeyFuel, buf.ReadUnsignedByte()*100+buf.ReadUnsignedByte())
		case 0x02:
			pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedByte()*100+buf.ReadUnsignedByte())
		case 0x05:
			switch buf.ReadUnsignedByte() {
			case 1:
				pos.Set("sign", true)
			case 2:
				pos.Set("sign", false)
			}
			pos.Set(model.KeyDriverUniqueID, buf.ReadString(dataLength-1))
		case 0x18:
			for i := 1; i <= 4; i++ {
				value := float64(buf.ReadUnsignedShort())
				if value > 0x0000 && value < 0xFFFF {
					pos.Set("fuel"+strconv.Itoa(i), value/0xFFFE)
				}
			}
		case 0x20:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		case 0x23:
			network := model.NewNetwork()
			count := buf.ReadUnsignedByte()
			for i := 0; i < count; i++ {
				mcc := buf.ReadUnsignedShort()
				mnc := buf.ReadUnsignedByte()
				lac := buf.ReadUnsignedShort()
				cid := buf.ReadUnsignedShort()
				network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), buf.ReadUnsignedByte()))
			}
			if count > 0 {
				pos.Network = network
			}
		}
	}
	return pos
}

func encodeMessage(command int, uniqueID string, content []byte) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0x29)
	buf.WriteByte8(0x29)
	buf.WriteByte8(command)
	buf.WriteShort(6 + len(content))

	uniqueID = "00000000" + uniqueID
	uniqueID = kit.SubstringFrom(uniqueID, kit.Len(uniqueID)-8)
	buf.WriteByte8(kit.Int(kit.Substring(uniqueID, 0, 2)))
	buf.WriteByte8(kit.Int(kit.Substring(uniqueID, 2, 4)) + 0x80)
	buf.WriteByte8(kit.Int(kit.Substring(uniqueID, 4, 6)) + 0x80)
	buf.WriteByte8(kit.Int(kit.Substring(uniqueID, 6, 8)))

	buf.WriteBytes(content)
	buf.WriteByte8(kit.XOR(buf.Bytes()))
	buf.WriteByte8(0x0D) // ending
	return buf
}

// encode is KhdProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	uniqueID := m.UniqueID(c.DeviceID)
	switch c.Type {
	case model.CommandEngineStop:
		return encodeMessage(msgCutOil, uniqueID, nil)
	case model.CommandEngineResume:
		return encodeMessage(msgResumeOil, uniqueID, nil)
	case model.CommandGetVersion:
		return encodeMessage(msgCheckVersion, uniqueID, nil)
	case model.CommandFactoryReset:
		return encodeMessage(msgFactoryReset, uniqueID, nil)
	case model.CommandSetSpeedLimit:
		// Java sends MSG_RESUME_OIL here, not MSG_SET_OVERSPEED
		return encodeMessage(msgResumeOil, uniqueID, []byte{byte(kit.CommandInt(c, model.CommandKeyData))})
	case model.CommandSetOdometer:
		return encodeMessage(msgDeleteMileage, uniqueID, nil)
	case model.CommandPositionSingle:
		return encodeMessage(msgOnDemandTrack, uniqueID, nil)
	}
	return nil
}
