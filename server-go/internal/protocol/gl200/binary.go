package gl200

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// This file is Gl200BinaryProtocolDecoder.

func decodeBinaryTime(buf *kit.Buf) time.Time {
	return kit.NewDateBuilder(nil).
		SetDate(buf.ReadUnsignedShort(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		Date()
}

const (
	msgRspLCB        = 3
	msgRspGEO        = 8
	msgRspCompressed = 100
)

func readIMEI(buf *kit.Buf) string { return kit.Format("%015d", buf.ReadLong()) }

func checkBit(v, i int) bool { return v&(1<<i) != 0 }

func decodeBinaryLocation(m *ingest.Message, buf *kit.Buf) any {
	positions := []*model.Position{}

	typ := buf.ReadUnsignedByte()

	buf.ReadUnsignedInt()   // mask
	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedByte()  // device type
	buf.ReadUnsignedShort() // protocol version
	buf.ReadUnsignedShort() // firmware version

	ds := m.Session(readIMEI(buf))
	if ds == nil {
		return nil
	}

	battery := buf.ReadUnsignedByte()
	power := buf.ReadUnsignedShort()

	if typ == msgRspGEO {
		buf.ReadUnsignedByte() // reserved
		buf.ReadUnsignedByte() // reserved
	}

	buf.ReadUnsignedByte() // motion status
	satellites := buf.ReadUnsignedByte()

	if typ != msgRspCompressed {
		buf.ReadUnsignedByte() // index
	}

	if typ == msgRspLCB {
		buf.ReadUnsignedByte() // phone length
		for b := buf.ReadUnsignedByte(); ; b = buf.ReadUnsignedByte() {
			if b&0xf == 0xf || b&0xf0 == 0xf0 {
				break
			}
		}
	}

	if typ == msgRspCompressed {
		count := buf.ReadUnsignedShort()

		var speed, heading, latitude, longitude int32
		var t int64

		for range count {
			if t > 0 {
				t++
			}

			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			switch buf.GetUnsignedByte(buf.ReaderIndex()) >> (8 - 2) {
			case 1:
				bits := kit.NewBitBuffer(buf.ReadSlice(3))
				bits.ReadUnsigned(2) // point attribute
				bits.ReadUnsigned(1) // fix type
				speed = int32(bits.ReadUnsigned(12))
				heading = int32(bits.ReadUnsigned(9))
				longitude = buf.ReadInt()
				latitude = buf.ReadInt()
				if t == 0 {
					t = buf.ReadUnsignedInt()
				}
			case 2:
				bits := kit.NewBitBuffer(buf.ReadSlice(5))
				bits.ReadUnsigned(2) // point attribute
				bits.ReadUnsigned(1) // fix type
				speed += int32(bits.ReadSigned(7))
				heading += int32(bits.ReadSigned(7))
				longitude += int32(bits.ReadSigned(12))
				latitude += int32(bits.ReadSigned(11))
			default:
				buf.ReadUnsignedByte() // invalid or same
				continue
			}

			pos.Valid = true
			pos.SetTime(time.UnixMilli(t * 1000))
			pos.Speed = units.KnotsFromKph(float64(speed) / 10.0)
			pos.Course = float64(heading)
			pos.SetLongitude(float64(longitude) / 1000000.0)
			pos.SetLatitude(float64(latitude) / 1000000.0)

			positions = append(positions, pos)
		}
		return positions
	}

	count := buf.ReadUnsignedByte()
	for range count {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyBatteryLevel, battery)
		pos.Set(model.KeyPower, power)
		pos.Set(model.KeySatellites, satellites)

		hdop := buf.ReadUnsignedByte()
		pos.Valid = hdop > 0
		pos.Set(model.KeyHdop, hdop)

		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedMedium()) / 10.0)
		pos.Course = float64(buf.ReadUnsignedShort())
		pos.Altitude = float64(buf.ReadShort())
		pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)

		pos.SetTime(decodeBinaryTime(buf))

		pos.Network = readCellTower(buf)

		buf.ReadUnsignedByte() // reserved

		positions = append(positions, pos)
	}
	return positions
}

func readCellTower(buf *kit.Buf) *model.Network {
	mcc := buf.ReadUnsignedShort()
	mnc := buf.ReadUnsignedShort()
	lac := buf.ReadUnsignedShort()
	cid := buf.ReadUnsignedShort()
	return model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
}

const (
	msgEvtBPL = 6
	msgEvtVGN = 45
	msgEvtVGF = 46
	msgEvtUPD = 15
	msgEvtIDF = 17
	msgEvtGSS = 21
	msgEvtGES = 26
	msgEvtGPJ = 31
	msgEvtRMD = 35
	msgEvtJDS = 33
	msgEvtCRA = 23
	msgEvtUPC = 34
)

func decodeBinaryEvent(m *ingest.Message, buf *kit.Buf) any {
	pos := m.NewPosition()

	typ := buf.ReadUnsignedByte()

	buf.ReadUnsignedInt()   // mask
	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedByte()  // device type
	buf.ReadUnsignedShort() // protocol version

	pos.Set(model.KeyVersionFw, kit.Format("%d", buf.ReadUnsignedShort()))

	ds := m.Session(readIMEI(buf))
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
	pos.Set(model.KeyPower, buf.ReadUnsignedShort())

	buf.ReadUnsignedByte() // motion status

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())

	switch typ {
	case msgEvtBPL:
		buf.ReadUnsignedShort() // backup battery voltage
	case msgEvtVGN, msgEvtVGF:
		buf.ReadUnsignedShort() // reserved
		buf.ReadUnsignedByte()  // report type
		buf.ReadUnsignedInt()   // ignition duration
	case msgEvtUPD:
		buf.ReadUnsignedShort() // code
		buf.ReadUnsignedByte()  // retry
	case msgEvtIDF:
		buf.ReadUnsignedInt() // idling duration
	case msgEvtGSS:
		buf.ReadUnsignedByte() // gps signal status
		buf.ReadUnsignedInt()  // reserved
	case msgEvtGES:
		buf.ReadUnsignedShort() // trigger geo id
		buf.ReadUnsignedByte()  // trigger geo enable
		buf.ReadUnsignedByte()  // trigger mode
		buf.ReadUnsignedInt()   // radius
		buf.ReadUnsignedInt()   // check interval
	case msgEvtGPJ:
		buf.ReadUnsignedByte() // cw jamming value
		buf.ReadUnsignedByte() // gps jamming state
	case msgEvtRMD:
		buf.ReadUnsignedByte() // roaming state
	case msgEvtJDS:
		buf.ReadUnsignedByte() // jamming state
	case msgEvtCRA:
		buf.ReadUnsignedByte() // crash counter
	case msgEvtUPC:
		buf.ReadUnsignedByte()  // command id
		buf.ReadUnsignedShort() // result
	}

	buf.ReadUnsignedByte() // count

	hdop := buf.ReadUnsignedByte()
	pos.Valid = hdop > 0
	pos.Set(model.KeyHdop, hdop)

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedMedium()) / 10.0)
	pos.Course = float64(buf.ReadUnsignedShort())
	pos.Altitude = float64(buf.ReadShort())
	pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
	pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)

	pos.SetTime(decodeBinaryTime(buf))

	pos.Network = readCellTower(buf)

	buf.ReadUnsignedByte() // reserved

	return pos
}

const (
	msgInfVER = 6
	msgInfBAT = 7
	msgInfCSQ = 5
	msgInfGIR = 10
)

func decodeBinaryInformation(m *ingest.Message, buf *kit.Buf) any {
	pos := m.NewPosition()

	typ := buf.ReadUnsignedByte()

	buf.ReadUnsignedInt()   // mask
	buf.ReadUnsignedShort() // length

	ds := m.Session(readIMEI(buf))
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedByte()  // device type
	buf.ReadUnsignedShort() // protocol version

	pos.Set(model.KeyVersionFw, kit.Format("%d", buf.ReadUnsignedShort()))

	if typ == msgInfVER {
		buf.ReadUnsignedShort() // hardware version
		buf.ReadUnsignedShort() // mcu version
		buf.ReadUnsignedShort() // reserved
	}

	buf.ReadUnsignedByte() // motion status
	buf.ReadUnsignedByte() // reserved

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())

	buf.ReadUnsignedByte()  // mode
	buf.Skip(7)             // last fix time
	buf.ReadUnsignedByte()  // reserved
	buf.ReadUnsignedByte()  //
	buf.ReadUnsignedShort() // response report mask
	buf.ReadUnsignedShort() // ign interval
	buf.ReadUnsignedShort() // igf interval
	buf.ReadUnsignedInt()   // reserved
	buf.ReadUnsignedByte()  // reserved

	if typ == msgInfBAT {
		pos.Set(model.KeyCharge, buf.ReadUnsignedByte() != 0)
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
		pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
	}

	buf.Skip(10) // iccid

	if typ == msgInfCSQ {
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		buf.ReadUnsignedByte()
	}

	buf.ReadUnsignedByte()  // time zone flags
	buf.ReadUnsignedShort() // time zone offset

	if typ == msgInfGIR {
		buf.ReadUnsignedByte() // gir trigger
		buf.ReadUnsignedByte() // cell number
		pos.Network = readCellTower(buf)
		buf.ReadUnsignedByte() // ta
		buf.ReadUnsignedByte() // rx level
	}

	t := decodeBinaryTime(buf)
	m.LastLocation(pos, &t)

	return pos
}

func readVariableLength(buf *kit.Buf) int {
	value := buf.ReadUnsignedByte()
	if checkBit(value, 7) {
		value = (value&0x7f)<<8 | buf.ReadUnsignedByte()
	}
	return value
}

func decodeBinaryFrame(m *ingest.Message, buf *kit.Buf) any {
	frameStart := buf.ReaderIndex()

	buf.ReadUnsignedByte() // header
	buf.ReadUnsignedByte() // identifier
	frameLength := buf.ReadUnsignedShort()

	if checkBit(buf.ReadUnsignedByte(), 7) { // multi-packet flag
		buf.ReadUnsignedShort() // frame count and frame number
	}

	imei := buf.ReadSlice(8).HexDump()[1:]
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	buf.ReadUnsignedShort() // device type
	buf.ReadUnsignedShort() // protocol version
	buf.ReadUnsignedByte()  // custom version

	buf.Skip(buf.ReadUnsignedByte()) // reserved field

	positions := []*model.Position{}

	recordsEnd := frameStart + frameLength - 4 // count number, check byte and tail

	for buf.ReaderIndex() < recordsEnd {
		recordStart := buf.ReaderIndex()
		recordEnd := recordStart + readVariableLength(buf)

		buf.ReadUnsignedInt()   // generated time
		buf.ReadUnsignedShort() // record count number
		buf.ReadUnsignedByte()  // record id
		buf.ReadUnsignedByte()  // event code

		for buf.ReaderIndex() < recordEnd {
			dataID := readVariableLength(buf)
			dataEnd := buf.ReaderIndex() + readVariableLength(buf)

			if dataID == 0x52 {
				pos := m.NewPosition()
				pos.DeviceID = ds.DeviceID

				pos.Valid = kit.BetweenInt(buf.ReadUnsignedByte(), 2, 4) == 0b10
				pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
				pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
				pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)

				pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
				pos.Course = float64(buf.ReadUnsignedShort())
				pos.Altitude = float64(buf.ReadUnsignedMedium()) / 10.0
				pos.Set(model.KeySatellites, buf.ReadUnsignedByte())

				positions = append(positions, pos)
			}

			buf.SetReaderIndex(dataEnd)
		}

		buf.SetReaderIndex(recordEnd)
	}

	return positions
}

// DecodeBinary is Gl200BinaryProtocolDecoder.decode.
func DecodeBinary(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.GetUnsignedByte(buf.ReaderIndex()+1) == 0 {
		return decodeBinaryFrame(m, buf)
	}

	switch buf.ReadString(4) {
	case "+RSP":
		return decodeBinaryLocation(m, buf)
	case "+INF":
		return decodeBinaryInformation(m, buf)
	case "+EVT":
		return decodeBinaryEvent(m, buf)
	}
	return nil
}
