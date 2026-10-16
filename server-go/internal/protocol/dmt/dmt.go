// Package dmt is the Digital Matter protocol (DmtProtocol): little-endian binary
// messages with a length field, over TCP.
package dmt

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
		Name: "dmt",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is the LengthFieldBasedFrameDecoder of DmtProtocol: a little-endian length at
// offset 3.
func Framer() ingest.Framer {
	f := ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 0, 0)
	f.LittleEndian = true
	return f
}

const (
	msgHello           = 0x00
	msgHelloResponse   = 0x01
	msgDataRecord      = 0x04
	msgCommit          = 0x05
	msgCommitResponse  = 0x06
	msgDataRecord64    = 0x10
	msgCannedRequest1  = 0x14
	msgCannedResponse1 = 0x15
	msgCannedRequest2  = 0x22
	msgCannedResponse2 = 0x23
)

// epoch2013 is the time base of the standard records (1 Jan 2013).
const epoch2013 = 1356998400000

func sendResponse(m *ingest.Message, typ int, content *kit.Buf) {
	response := kit.Empty()
	response.WriteByte8(0x02).WriteByte8(0x55) // header
	response.WriteByte8(typ)
	if content != nil {
		response.WriteShortLE(content.ReadableBytes())
		response.WriteBuf(content)
	} else {
		response.WriteShortLE(0)
	}
	m.Send(response)
}

func decodeFixed64(m *ingest.Message, buf *kit.Buf) any {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	positions := []*model.Position{}
	for buf.ReadableBytes() >= 64 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		buf.ReadSignedByte() // type
		pos.Set(model.KeyIndex, buf.ReadUnsignedIntLE())

		t := buf.ReadUnsignedIntLE()
		pos.SetTime(kit.NewDateBuilder(nil).
			SetYear(int(2000 + (t & 0x3F))).
			SetMonth(int(t>>6) & 0xF).
			SetDay(int(t>>10) & 0x1F).
			SetHour(int(t>>15) & 0x1F).
			SetMinute(int(t>>20) & 0x3F).
			SetSecond(int(t>>26) & 0x3F).
			Date())

		pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.Speed = units.KnotsFromCps(float64(buf.ReadUnsignedShortLE()))
		pos.Course = float64(buf.ReadUnsignedByte() * 2)
		pos.Altitude = float64(buf.ReadShortLE())

		buf.ReadUnsignedShortLE() // position accuracy
		buf.ReadUnsignedByte()    // speed accuracy

		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
		pos.Valid = kit.Check(int64(buf.ReadSignedByte()), 0)
		pos.Set(model.KeyInput, buf.ReadUnsignedIntLE())
		pos.Set(model.KeyOutput, buf.ReadUnsignedShortLE())
		for i := 1; i <= 5; i++ {
			pos.Set(model.PrefixAdc+strconv.Itoa(i), buf.ReadShortLE())
		}
		pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))

		buf.ReadShortLE() // accelerometer x
		buf.ReadShortLE() // accelerometer y
		buf.ReadShortLE() // accelerometer z
		buf.Skip(8)       // device id

		pos.Set(model.KeyPdop, float64(buf.ReadUnsignedShortLE())/100.0)

		buf.Skip(2)               // reserved
		buf.ReadUnsignedShortLE() // checksum

		positions = append(positions, pos)
	}
	return positions
}

func decodeAlarm(value int) string {
	switch value {
	case 12:
		return model.AlarmBraking
	case 13:
		return model.AlarmAcceleration
	case 14:
		return model.AlarmCornering
	case 18:
		return model.AlarmOverspeed
	case 20:
		return model.AlarmTow
	case 23:
		return model.AlarmAccident
	case 29:
		return model.AlarmTampering
	case 44:
		return model.AlarmGeofenceEnter
	case 45:
		return model.AlarmGeofenceExit
	}
	return ""
}

func decodeStandard(m *ingest.Message, buf *kit.Buf) any {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	positions := []*model.Position{}
	for buf.IsReadable() {
		recordEnd := buf.ReaderIndex() + buf.ReadUnsignedShortLE()

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyIndex, buf.ReadUnsignedIntLE())
		pos.DeviceTime = model.NewTime(time.UnixMilli(epoch2013 + buf.ReadUnsignedIntLE()*1000))

		event := buf.ReadUnsignedByte()
		pos.AddAlarm(decodeAlarm(event))
		pos.Set(model.KeyEvent, event)

		for buf.ReaderIndex() < recordEnd {
			fieldID := buf.ReadUnsignedByte()
			fieldLength := buf.ReadUnsignedByte()
			fieldEnd := buf.ReaderIndex()
			if fieldLength == 255 {
				fieldEnd += buf.ReadUnsignedShortLE()
			} else {
				fieldEnd += fieldLength
			}

			switch fieldID {
			case 0:
				pos.FixTime = model.NewTime(time.UnixMilli(epoch2013 + buf.ReadUnsignedIntLE()*1000))
				pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.Altitude = float64(buf.ReadShortLE())
				pos.Speed = units.KnotsFromCps(float64(buf.ReadUnsignedShortLE()))
				buf.ReadUnsignedByte() // speed accuracy
				pos.Course = float64(buf.ReadUnsignedByte() * 2)
				pos.Set(model.KeyPdop, float64(buf.ReadUnsignedByte())/10.0)
				pos.Accuracy = float64(buf.ReadUnsignedByte())
				pos.Valid = buf.ReadUnsignedByte() != 0
			case 2:
				input := int(buf.ReadIntLE())
				output := buf.ReadUnsignedShortLE()
				status := buf.ReadUnsignedShortLE()
				pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))
				if !kit.Check(int64(status), 1) {
					pos.AddAlarm(model.AlarmLowBattery)
				} else if kit.Check(int64(status), 6) {
					pos.AddAlarm(model.AlarmTampering)
				}
				pos.Set(model.KeyInput, input)
				pos.Set(model.KeyOutput, output)
				pos.Set(model.KeyStatus, status)
			case 3:
				var driverID string
				switch buf.ReadUnsignedByte() {
				case 1:
					driverID = buf.ReadSlice(5).HexDump()
				case 2:
					driverID = buf.ReadSlice(6).HexDump()
				case 3:
					driverID = buf.ReadString(4)
				case 4:
					driverID = buf.ReadString(5)
				case 5, 6:
					driverID = buf.ReadString(fieldLength - 1)
				case 7:
					driverID = buf.ReadSlice(fieldLength - 1).HexDump()
				}
				pos.Set(model.KeyDriverUniqueID, driverID)
			case 6:
				for buf.ReaderIndex() < fieldEnd {
					number := buf.ReadUnsignedByte()
					switch number {
					case 1:
						pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)
					case 2:
						pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/100.0)
					case 3:
						pos.Set(model.KeyDeviceTemp, float64(buf.ReadShortLE())/100.0)
					case 4:
						pos.Set(model.KeyRssi, buf.ReadUnsignedShortLE())
					case 5:
						pos.Set("solarPower", float64(buf.ReadUnsignedShortLE())/1000.0)
					default:
						pos.Set(model.PrefixIo+strconv.Itoa(number), buf.ReadUnsignedShortLE())
					}
				}
			case 26:
				pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedIntLE())
				pos.Set("tripHours", buf.ReadUnsignedIntLE()*1000)
			case 27:
				pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
				pos.Set(model.KeyHours, buf.ReadUnsignedIntLE()*1000)
			}

			buf.SetReaderIndex(fieldEnd)
		}

		if pos.FixTime.IsZero() {
			deviceTime := pos.DeviceTime.Time
			m.LastLocation(pos, &deviceTime)
		}
		positions = append(positions, pos)
	}
	return positions
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2) // header
	typ := buf.ReadUnsignedByte()
	length := buf.ReadUnsignedShortLE()

	switch typ {
	case msgHello:
		buf.ReadUnsignedIntLE() // device serial number
		ds := m.Session(kit.Latin1(buf.ReadSlice(15).Bytes()))
		response := kit.Empty()
		if length == 51 {
			response.WriteByte8(0) // reserved
			response.WriteIntLE(0) // reserved
		} else {
			response.WriteIntLE((m.Now.UnixMilli() - epoch2013) / 1000)
			flags := int64(1)
			if ds != nil {
				flags = 0
			}
			response.WriteIntLE(flags)
		}
		sendResponse(m, msgHelloResponse, response)
	case msgCommit:
		sendResponse(m, msgCommitResponse, kit.Empty().WriteByte8(1)) // flags (success)
	case msgCannedRequest1:
		sendResponse(m, msgCannedResponse1, kit.Empty().WriteZero(12))
	case msgCannedRequest2:
		sendResponse(m, msgCannedResponse2, nil)
	case msgDataRecord64:
		return decodeFixed64(m, buf)
	case msgDataRecord:
		return decodeStandard(m, buf)
	}
	return nil
}
