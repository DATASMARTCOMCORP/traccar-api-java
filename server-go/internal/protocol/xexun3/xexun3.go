// Package xexun3 is the Xexun3 protocol (Xexun3Protocol): length-prefixed binary frames
// (0xfc header) of tagged records, over TCP.
package xexun3

import (
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

const (
	msgData    = 0x20
	msgCommand = 0x21
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "xexun3",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(Decode),
			}
		}}},
	}
}

// Framer is the protocol's LengthFieldBasedFrameDecoder(MAX_FRAME_LENGTH, 1, 2, 3, 0).
func Framer() ingest.Framer { return ingest.NewLengthFramer(ingest.MaxFrameLength, 1, 2, 3, 0) }

func sendResponse(m *ingest.Message, typ, index int, imei *kit.Buf) {
	response := kit.Empty()
	response.WriteByte8(0xFC)
	response.WriteShort(12)   // length
	response.WriteByte8(0x03) // version
	response.WriteByte8(typ)
	response.WriteByte8(index)
	response.WriteBytes(imei.GetBytes(imei.ReaderIndex(), 8))
	response.WriteByte8(0) // result
	response.WriteShort(kit.CRC16(kit.CRC16_CCITT_FALSE, response.GetBytes(3, 12)))
	response.WriteByte8(0xCF)
	m.Send(response)
}

func decodeAlarm(value int) string {
	switch value {
	case 1:
		return model.AlarmSos
	case 2:
		return model.AlarmLowBattery
	case 4:
		return model.AlarmRemoving
	case 5:
		return model.AlarmMovement
	case 7:
		return model.AlarmFallDown
	case 8:
		return model.AlarmAccident
	case 11:
		return model.AlarmPowerRestored
	case 12:
		return model.AlarmPowerCut
	case 13:
		return model.AlarmPowerOn
	case 14:
		return model.AlarmPowerOff
	case 15, 16:
		return model.AlarmDoor
	}
	return ""
}

var macPairs = regexp.MustCompile("(..)")

func readTime(buf *kit.Buf) model.Time {
	return model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
}

// Decode is Xexun3ProtocolDecoder.
func Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header
	length := buf.ReadUnsignedShort()
	buf.ReadUnsignedByte() // version
	typ := buf.ReadUnsignedByte()
	index := buf.ReadUnsignedByte()

	imei := buf.ReadSlice(8)
	ds := m.Session(kit.SubstringFrom(imei.HexDump(), 1))
	if ds == nil {
		return nil
	}

	if typ != msgCommand {
		sendResponse(m, typ, index, imei)
	}

	if typ != msgData {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	network := model.NewNetwork()
	hasLocation := false

	bodyEnd := buf.ReaderIndex() + length - 11
	for buf.ReaderIndex() < bodyEnd {
		subType := buf.ReadUnsignedByte()
		subLength := buf.ReadUnsignedByte()
		subEnd := buf.ReaderIndex() + subLength

		switch subType {
		case 0x64:
			pos.SetTime(readTime(buf).Time)
			pos.Valid = true
			pos.SetLatitude(buf.ReadDouble())
			pos.SetLongitude(buf.ReadDouble())
			pos.Altitude = float64(buf.ReadFloat())
			buf.ReadUnsignedByte() // ephemeris
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			buf.ReadUnsignedByte() // signal
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
			hasLocation = true
		case 0x65:
			pos.DeviceTime = readTime(buf)
			count := buf.ReadUnsignedByte()
			for i := 0; i < count; i++ {
				mac := macPairs.ReplaceAllString(buf.ReadSlice(6).HexDump(), "$1:")
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac[:len(mac)-1], int(buf.ReadSignedByte())))
			}
		case 0x66:
			pos.DeviceTime = readTime(buf)
			mcc := buf.ReadUnsignedShort()
			mnc := buf.ReadUnsignedShort()
			lac := int(buf.ReadInt())
			cid := buf.ReadUnsignedInt()
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, buf.ReadUnsignedByte()))
		case 0x69:
			pos.DeviceTime = readTime(buf)
			alarmID := buf.ReadUnsignedByte()
			dataLength := buf.ReadUnsignedByte()
			pos.AddAlarm(decodeAlarm(alarmID))
			if alarmID == 17 && dataLength > 0 {
				pos.Set(model.KeyIccid, buf.ReadSlice(dataLength).HexDump())
			}
		case 0x6A:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			buf.ReadUnsignedShort() // network duration
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			buf.ReadUnsignedByte() // track sequence
			pos.Set(model.KeyMotion, buf.ReadUnsignedByte() > 0)
			pos.Set(model.KeyCharge, buf.ReadUnsignedByte() > 0)
			if steps := buf.ReadUnsignedShort(); steps != 0xFFFF {
				pos.Set(model.KeySteps, steps)
			}
			if temperature := int(buf.ReadSignedByte()); temperature != -1 {
				pos.Set("temperature", temperature)
			}
			buf.ReadUnsignedByte() // respiratory rate
			if heartRate := buf.ReadUnsignedByte(); heartRate != 0xFF {
				pos.Set(model.KeyHeartRate, heartRate)
			}
			buf.ReadUnsignedByte() // blood pressure systolic
			buf.ReadUnsignedByte() // blood pressure diastolic
			buf.ReadUnsignedByte() // blood oxygen
			if fuel := buf.ReadUnsignedByte(); fuel != 0xFF {
				pos.Set(model.KeyFuelLevel, fuel)
			}
		}

		buf.SetReaderIndex(subEnd)
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}

	if pos.DeviceTime.IsZero() {
		return nil
	}

	if !hasLocation {
		t := pos.DeviceTime.Time
		m.LastLocation(pos, &t)
	}

	return pos
}
