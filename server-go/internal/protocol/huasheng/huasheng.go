// Package huasheng is the HuaSheng protocol (HuaShengProtocol): binary frames between
// 0xC0 markers (SLIP-style escapes) with logins, positions with OBD data and fault codes.
package huasheng

import (
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "huasheng",
		DataCommands: protocol.DataCommands("huasheng"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Framer is HuaShengFrameDecoder: up to the next 0xC0, with DB DC as C0 and DB DD as DB.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}
		index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), 0xC0)
		if index == -1 {
			return nil
		}
		result := kit.Empty()
		for buf.ReaderIndex() <= index {
			b := buf.ReadUnsignedByte()
			if b == 0xDB {
				switch buf.ReadUnsignedByte() {
				case 0xDC:
					result.WriteByte8(0xC0)
				case 0xDD:
					result.WriteByte8(0xDB)
				}
			} else {
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

const (
	msgPosition    = 0xAA00
	msgPositionRsp = 0xFF01
	msgLogin       = 0xAA02
	msgLoginRsp    = 0xFF03
	msgUpfault     = 0xAA12
	msgHsoReq      = 0x0002
	msgHsoRsp      = 0x0003
	msgSetReq      = 0xAA04
	msgCtrlReq     = 0xAA16
)

func sendResponse(m *ingest.Message, msgType int, index int32, content *kit.Buf) {
	response := kit.Empty()
	response.WriteByte8(0xC0)
	response.WriteShort(0x0100)
	length := 12
	if content != nil {
		length += content.ReadableBytes()
	}
	response.WriteShort(length)
	response.WriteShort(msgType)
	response.WriteShort(0)
	response.WriteInt(int64(index))
	if content != nil {
		response.WriteBuf(content)
	}
	response.WriteByte8(0xC0)
	m.Send(response)
}

func decodeAlarm(event int) string {
	switch event {
	case 4:
		return model.AlarmFatigueDriving
	case 6:
		return model.AlarmSos
	case 7:
		return model.AlarmBraking
	case 8:
		return model.AlarmAcceleration
	case 9:
		return model.AlarmCornering
	case 10:
		return model.AlarmAccident
	case 16:
		return model.AlarmRemoving
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(1)                // start marker
	buf.ReadUnsignedByte()     // flag
	buf.ReadUnsignedByte()     // reserved
	buf.ReadUnsignedShort()    // length
	msgType := buf.ReadUnsignedShort()
	buf.ReadUnsignedShort() // checksum
	index := buf.ReadInt()

	switch msgType {
	case msgLogin:
		for buf.ReadableBytes() > 4 {
			subtype := buf.ReadUnsignedShort()
			length := buf.ReadUnsignedShort() - 4
			if subtype == 0x0003 {
				imei := buf.ReadString(length)
				if ds := m.Session(imei); ds != nil {
					content := kit.Empty()
					content.WriteByte8(0) // success
					sendResponse(m, msgLoginRsp, index, content)
				}
			} else {
				buf.Skip(length)
			}
		}
	case msgHsoReq:
		sendResponse(m, msgHsoRsp, index, nil)
	case msgUpfault:
		if pos := decodeFaultCodes(m, buf); pos != nil {
			return pos
		}
	case msgPosition:
		if pos := decodePosition(m, buf, index); pos != nil {
			return pos
		}
	}
	return nil
}

func decodeFaultCodes(m *ingest.Message, buf *kit.Buf) *model.Position {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	buf.ReadUnsignedShort() // type
	buf.ReadUnsignedShort() // length

	var codes strings.Builder
	for buf.ReadableBytes() > 2 {
		value := buf.ReadSlice(2).HexDump()
		digit := kit.IntRadix(kit.Substring(value, 0, 1), 16)
		prefix := 'P'
		switch digit >> 2 {
		case 1:
			prefix = 'C'
		case 2:
			prefix = 'B'
		case 3:
			prefix = 'U'
		}
		codes.WriteRune(prefix)
		codes.WriteString(strconv.Itoa(digit % 4))
		codes.WriteString(kit.SubstringFrom(value, 1))
		if buf.ReadableBytes() > 2 {
			codes.WriteByte(' ')
		}
	}
	pos.Set(model.KeyDtcs, codes.String())
	return pos
}

func decodePosition(m *ingest.Message, buf *kit.Buf, index int32) *model.Position {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	status := buf.ReadUnsignedShort()
	pos.Valid = kit.Check(int64(status), 15)
	pos.Set(model.KeyStatus, status)
	pos.Set(model.KeyIgnition, kit.Check(int64(status), 14))

	event := buf.ReadUnsignedShort()
	pos.AddAlarm(decodeAlarm(event))
	pos.Set(model.KeyEvent, event)

	t := buf.ReadString(12)
	b := kit.NewDateBuilder(nil)
	b.SetYear(kit.Int(kit.Substring(t, 0, 2)))
	b.SetMonth(kit.Int(kit.Substring(t, 2, 4)))
	b.SetDay(kit.Int(kit.Substring(t, 4, 6)))
	b.SetHour(kit.Int(kit.Substring(t, 6, 8)))
	b.SetMinute(kit.Int(kit.Substring(t, 8, 10)))
	b.SetSecond(kit.Int(kit.Substring(t, 10, 12)))
	pos.SetTime(b.Date())

	pos.SetLongitude(float64(buf.ReadInt()) / 100000.0)
	pos.SetLatitude(float64(buf.ReadInt()) / 100000.0)

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
	pos.Course = float64(buf.ReadUnsignedShort())
	pos.Altitude = float64(buf.ReadUnsignedShort())

	buf.ReadUnsignedShort() // odometer speed

	network := model.NewNetwork()

	for buf.ReadableBytes() > 4 {
		subtype := buf.ReadUnsignedShort()
		length := buf.ReadUnsignedShort() - 4
		endIndex := buf.ReaderIndex() + length
		switch subtype {
		case 0x0001:
			coolantTemperature := buf.ReadUnsignedByte() - 40
			if coolantTemperature <= 215 {
				pos.Set(model.KeyCoolantTemp, coolantTemperature)
			}
			pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
			pos.Set("averageSpeed", buf.ReadUnsignedByte())
			buf.ReadUnsignedShort() // interval fuel consumption
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())/100.0)
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedShort())
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
			pos.Set(model.KeyFuel, float64(buf.ReadUnsignedByte())*0.4)
			if endIndex-buf.ReaderIndex() >= 7 {
				pos.Set("fuelLevel2", buf.ReadUnsignedShort())
			}
			buf.ReadUnsignedInt() // trip id
			if buf.ReaderIndex() < endIndex {
				pos.Set("adBlueLevel", float64(buf.ReadUnsignedByte())*0.4)
			}
		case 0x0005:
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyHdop, buf.ReadUnsignedByte())
			buf.ReadUnsignedInt() // run time
		case 0x0009:
			pos.Set(model.KeyVin, buf.ReadString(length))
		case 0x0010:
			pos.Set(model.KeyOdometer, kit.Double(buf.ReadString(length))*1000)
		case 0x0011:
			pos.Set(model.KeyHours, float64(buf.ReadUnsignedInt())/20.0)
		case 0x0014:
			buf.ReadUnsignedByte() // valid flag
			pos.Set(model.KeyEngineLoad, float64(buf.ReadUnsignedByte())/255.0)
			pos.Set("timingAdvance", float64(buf.ReadUnsignedByte())*0.5)
			pos.Set("airTemp", buf.ReadUnsignedByte()-40)
			pos.Set("airFlow", float64(buf.ReadUnsignedShort())/100.0)
			pos.Set(model.KeyThrottle, float64(buf.ReadUnsignedByte())/255.0)
		case 0x0020:
			for _, cell := range kit.Split(buf.ReadString(length), "+") {
				values := kit.Split(cell, "@")
				network.AddCellTower(model.NewCellTower(
					kit.Int(values[0]), kit.Int(values[1]),
					kit.IntRadix(values[2], 16), kit.LongRadix(values[3], 16)))
			}
		case 0x0021:
			for _, point := range kit.Split(buf.ReadString(length), "+") {
				values := kit.Split(point, "@")
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(values[0], kit.Int(values[1])))
			}
		default:
			buf.Skip(length)
		}
		buf.SetReaderIndex(endIndex)
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}

	sendResponse(m, msgPositionRsp, index, nil)

	return pos
}

func encodeContent(msgType int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0xC0)
	buf.WriteShort(0x0000) // flag and version
	buf.WriteShort(12 + content.ReadableBytes())
	buf.WriteShort(msgType)
	buf.WriteShort(0) // checksum
	buf.WriteInt(1)   // index
	buf.WriteBuf(content)
	buf.WriteByte8(0xC0)
	return buf
}

func encode(_ *ingest.Message, c *model.Command) any {
	content := kit.Empty()
	switch c.Type {
	case model.CommandPositionPeriodic:
		content.WriteShort(0x0002)
		content.WriteShort(6) // length
		content.WriteShort(kit.CommandInt(c, model.CommandKeyFrequency))
		return encodeContent(msgSetReq, content)
	case model.CommandOutputControl:
		content.WriteByte8((kit.CommandInt(c, model.CommandKeyIndex)-1)*2 + (2 - kit.CommandInt(c, model.CommandKeyData)))
		return encodeContent(msgCtrlReq, content)
	case model.CommandAlarmArm, model.CommandAlarmDisarm:
		content.WriteShort(0x0001)
		content.WriteShort(5) // length
		if c.Type == model.CommandAlarmArm {
			content.WriteByte8(1)
		} else {
			content.WriteByte8(0)
		}
		return encodeContent(msgSetReq, content)
	case model.CommandSetSpeedLimit:
		content.WriteShort(0x0004)
		content.WriteShort(6) // length
		content.WriteShort(kit.CommandInt(c, model.CommandKeyData))
		return encodeContent(msgSetReq, content)
	}
	return nil
}
