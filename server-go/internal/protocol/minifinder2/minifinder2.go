// Package minifinder2 is the MiniFinder 2 protocol (Minifinder2Protocol): 0xAB binary
// frames of key-value records, with responses and binary commands.
package minifinder2

import (
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "minifinder2",
		DataCommands: protocol.DataCommands("minifinder2"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: &Encoder{},
			}
		}}},
	}
}

// Framer is the pipeline's LengthFieldBasedFrameDecoder(LITTLE_ENDIAN, 32768, 2, 2, 4, 0,
// true).
func Framer() ingest.Framer {
	f := ingest.NewLengthFramer(ingest.MaxFrameLengthLarge, 2, 2, 4, 0)
	f.LittleEndian = true
	return f
}

// Message types.
const (
	msgData          = 0x01
	msgConfiguration = 0x02
	msgServices      = 0x03
	msgSystemControl = 0x04
	msgFirmware      = 0x7E
	msgResponse      = 0x7F
)

// frame wraps content in the 0xAB header with its CRC and the index.
func frame(index int, content []byte) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0xAB) // header
	buf.WriteByte8(0x00) // properties
	buf.WriteShortLE(len(content))
	buf.WriteShortLE(kit.CRC16(kit.CRC16_XMODEM, content))
	buf.WriteShortLE(index)
	buf.WriteBytes(content)
	return buf
}

func sendResponse(m *ingest.Message, index, typ int, buf *kit.Buf) {
	if !m.HasChannel() {
		return
	}
	body := kit.Empty()
	if typ == msgServices {
		for buf.IsReadable() {
			end := buf.ReadUnsignedByte() + buf.ReaderIndex()
			key := buf.ReadUnsignedByte()
			switch key {
			case 0x11:
				body.WriteByte8(9 + 1) // length
				body.WriteByte8(key)
				body.WriteIntLE(0) // latitude
				body.WriteIntLE(0) // longitude
				body.WriteByte8(0) // address
			case 0x12:
				body.WriteByte8(5) // length
				body.WriteByte8(key)
				body.WriteIntLE(int64(int32(m.Now.UnixMilli() / 1000)))
			}
			buf.SetReaderIndex(end)
		}
	}

	content := kit.Empty()
	if body.IsReadable() {
		content.WriteByte8(msgServices)
	} else {
		content.WriteByte8(msgResponse)
		body.WriteByte8(1) // key length
		body.WriteByte8(0) // success
	}
	content.WriteBuf(body)

	m.Send(frame(index, content.Bytes()))
}

func readTagID(buf *kit.Buf) string {
	var parts [6]string
	for i := 5; i >= 0; i-- {
		parts[i] = buf.ReadSlice(1).HexDump()
	}
	return strings.Join(parts[:], "")
}

func colonPairs(hexText string) string {
	var b strings.Builder
	for k := 0; k < len(hexText); k += 2 {
		if k > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hexText[k : k+2])
	}
	return b.String()
}

func fromSeconds(seconds int64) time.Time { return time.UnixMilli(seconds * 1000) }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header
	flags := buf.ReadUnsignedByte()
	buf.ReadUnsignedShortLE() // length
	buf.ReadUnsignedShortLE() // checksum
	index := buf.ReadUnsignedShortLE()
	typ := buf.ReadUnsignedByte()

	if kit.Check(int64(flags), 4) {
		sendResponse(m, index, typ, buf.Duplicate())
	}

	switch typ {
	case msgData, msgServices:
		return decodeData(m, buf)
	case msgConfiguration:
		if pos := decodeConfiguration(m, buf); pos != nil {
			return pos
		}
		return nil
	case msgResponse:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		buf.ReadUnsignedByte() // length
		pos.Set(model.KeyResult, strconv.Itoa(buf.ReadUnsignedByte()))
		return pos
	}
	return nil
}

func decodeData(m *ingest.Message, buf *kit.Buf) any {
	var positions []*model.Position
	keys := map[int]bool{}
	pos := m.NewPosition()

	var ds *ingest.DeviceSession

	for buf.IsReadable() {
		length := buf.ReadUnsignedByte()
		end := buf.ReaderIndex() + length
		key := buf.ReadUnsignedByte()

		if key != 0x28 && key != 0x2c && keys[key] {
			positions = append(positions, pos)
			clear(keys)
			pos = m.NewPosition()
		}
		keys[key] = true

		switch key {
		case 0x01:
			ds = m.Session(buf.ReadString(15))
			if ds == nil {
				return nil
			}
		case 0x02:
			alarm := buf.ReadUnsignedIntLE()
			if kit.Check(alarm, 0) {
				pos.AddAlarm(model.AlarmLowBattery)
			}
			if kit.Check(alarm, 1) {
				pos.AddAlarm(model.AlarmOverspeed)
			}
			if kit.Check(alarm, 2) {
				pos.AddAlarm(model.AlarmFallDown)
			}
			for i := 0; i < 4; i++ {
				if kit.Check(alarm, i+4) {
					if kit.Check(alarm, i+26) {
						pos.AddAlarm(model.AlarmGeofenceEnter)
					} else {
						pos.AddAlarm(model.AlarmGeofenceExit)
					}
					pos.Set(model.KeyGeofence, i+1)
				}
			}
			if kit.Check(alarm, 8) {
				pos.AddAlarm(model.AlarmPowerOff)
			}
			if kit.Check(alarm, 9) {
				pos.AddAlarm(model.AlarmPowerOn)
			}
			if kit.Check(alarm, 10) {
				pos.AddAlarm(model.AlarmMovement)
			}
			if kit.Check(alarm, 12) {
				pos.AddAlarm(model.AlarmSos)
			}
			if kit.Check(alarm, 13) {
				pos.Set("button1", true)
			}
			if kit.Check(alarm, 14) {
				pos.Set("button2", true)
			}
			if kit.Check(alarm, 31) {
				pos.Set("bark", true)
			}
			if length >= 9 {
				pos.DeviceTime = model.NewTime(fromSeconds(buf.ReadUnsignedIntLE()))
			}
			pos.Set(model.KeyEvent, alarm)
		case 0x14:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)
		case 0x20:
			pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
			pos.Course = float64(buf.ReadUnsignedShortLE())
			pos.Altitude = float64(buf.ReadShortLE())
			hdop := buf.ReadUnsignedShortLE()
			pos.Valid = hdop > 0
			pos.Set(model.KeyHdop, float64(hdop)/10.0)
			pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		case 0x21, 0x29, 0x2B:
			mcc := buf.ReadUnsignedShortLE()
			var mnc int
			if key == 0x2B {
				mnc = buf.ReadUnsignedShortLE()
			} else {
				mnc = buf.ReadUnsignedByte()
			}
			if pos.Network == nil {
				pos.Network = model.NewNetwork()
			}
			for buf.ReaderIndex() < end {
				rssi := int(buf.ReadSignedByte())
				lac := buf.ReadUnsignedShortLE()
				var cid int64
				if key == 0x29 || key == 0x2B {
					cid = int64(buf.ReadIntLE())
				} else {
					cid = int64(buf.ReadUnsignedShortLE())
				}
				pos.Network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, rssi))
			}
		case 0x19, 0x22:
			if pos.Network == nil {
				pos.Network = model.NewNetwork()
			}
			for buf.ReaderIndex() < end {
				rssi := int(buf.ReadSignedByte())
				mac := colonPairs(buf.ReadSlice(6).HexDump())
				pos.Network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, rssi))
				if key == 0x19 {
					buf.Skip(buf.ReadUnsignedByte()) // name
				}
			}
		case 0x23, 0x26:
			if length >= 7 {
				pos.Set("tagId", readTagID(buf))
			}
			if length >= 15 {
				pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.Valid = true
			}
			if key == 0x26 {
				pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShortLE())/10.0)
				pos.Altitude = float64(buf.ReadShortLE())
			} else if buf.ReaderIndex() < end {
				pos.Set("description", buf.ReadString(end-buf.ReaderIndex()))
			}
		case 0x24:
			pos.SetTime(fromSeconds(buf.ReadUnsignedIntLE()))
			status := buf.ReadUnsignedIntLE()
			if kit.Check(status, 4) {
				pos.Set(model.KeyCharge, true)
			}
			if kit.Check(status, 7) {
				pos.Set(model.KeyArchive, true)
			}
			pos.Set(model.KeyMotion, kit.Check(status, 9))
			pos.Set(model.KeyRssi, kit.Between(status, 19, 24))
			pos.Set(model.KeyBatteryLevel, kit.From(status, 24))
			pos.Set(model.KeyStatus, status)
		case 0x25:
			pos.SetTime(fromSeconds(buf.ReadUnsignedIntLE()))
			pos.Set("callStatus", buf.ReadUnsignedByte())
			pos.Set("callDuration", buf.ReadUnsignedShortLE())
			pos.Set("callResult", buf.ReadUnsignedByte())
			pos.Set(model.KeyPhone, buf.ReadString(end-buf.ReaderIndex()))
		case 0x27:
			pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
			pos.Valid = true
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShortLE())/10.0)
			pos.Altitude = float64(buf.ReadShortLE())
		case 0x28, 0x2c:
			beaconFlags := buf.ReadUnsignedByte()
			beaconIndex := strconv.Itoa(kit.ToInt(beaconFlags, 4) + 1)
			pos.Set("tagId"+beaconIndex, readTagID(buf))
			pos.Set("tagRssi"+beaconIndex, int(buf.ReadSignedByte()))
			pos.Set("tag1mRssi"+beaconIndex, int(buf.ReadSignedByte()))
			if key == 0x2c {
				pos.Set("tagBattery"+beaconIndex, buf.ReadUnsignedByte())
			}
			if kit.Check(int64(beaconFlags), 7) {
				pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.Valid = true
			}
			if kit.Check(int64(beaconFlags), 6) {
				var descriptionLength int
				if key == 0x2c {
					descriptionLength = buf.ReadUnsignedByte()
				} else {
					descriptionLength = end - buf.ReaderIndex()
				}
				pos.Set("description"+beaconIndex, buf.ReadString(descriptionLength))
			}
			if key == 0x2c && buf.ReaderIndex() < end {
				pos.Set("tagTemp"+beaconIndex, float64(buf.ReadShort())/10.0)
			}
		case 0x2A:
			wifiFlags := buf.ReadUnsignedByte()
			buf.Skip(6)            // mac
			buf.ReadUnsignedByte() // rssi
			if kit.Check(int64(wifiFlags), 7) {
				pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
				pos.Valid = true
			}
			if kit.Check(int64(wifiFlags), 6) && end > buf.ReaderIndex() {
				pos.Set("description", buf.ReadString(end-buf.ReaderIndex()))
			}
		case 0x30:
			buf.ReadUnsignedIntLE() // timestamp
			pos.Set(model.KeySteps, buf.ReadUnsignedIntLE())
		case 0x31:
			for i := 1; buf.ReaderIndex() < end; i++ {
				pos.Set("activity"+strconv.Itoa(i)+"Time", buf.ReadUnsignedIntLE())
				pos.Set("activity"+strconv.Itoa(i), buf.ReadUnsignedIntLE())
			}
		case 0x37:
			buf.ReadUnsignedIntLE() // timestamp
			barking := buf.ReadUnsignedIntLE()
			if kit.Check(barking, 31) {
				pos.Set("barkStop", true)
			}
			pos.Set("barkCount", kit.To(barking, 31))
		case 0x40:
			buf.ReadUnsignedIntLE() // timestamp
			if heartRate := buf.ReadUnsignedByte(); heartRate > 1 {
				pos.Set(model.KeyHeartRate, heartRate)
			}
		case 0x41:
			buf.ReadUnsignedIntLE() // timestamp
			if spO2 := buf.ReadUnsignedByte(); spO2 > 1 {
				pos.Set("spO2", spO2)
			}
		}
		buf.SetReaderIndex(end)
	}

	positions = append(positions, pos)

	if ds == nil {
		return nil
	}
	for _, p := range positions {
		p.DeviceID = ds.DeviceID
		if !p.Valid && !p.Attributes.Has(model.KeyHdop) {
			m.LastLocation(p, nil)
		}
	}
	return positions
}

func decodeConfiguration(m *ingest.Message, buf *kit.Buf) *model.Position {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	for buf.IsReadable() {
		length := buf.ReadUnsignedByte() - 1
		end := buf.ReaderIndex() + length + 1
		key := buf.ReadUnsignedByte()

		switch key {
		case 0x01:
			pos.Set("moduleNumber", buf.ReadUnsignedInt())
		case 0x02:
			pos.Set(model.KeyVersionFw, strconv.FormatInt(buf.ReadUnsignedInt(), 10))
		case 0x03:
			pos.Set("imei", buf.ReadString(length))
		case 0x04:
			pos.Set(model.KeyIccid, buf.ReadString(length))
		case 0x05:
			pos.Set("bleMac", buf.ReadSlice(length).HexDump())
		case 0x06:
			pos.Set("settingTime", buf.ReadUnsignedInt())
		case 0x07:
			pos.Set("runTimes", buf.ReadUnsignedInt())
		case 0x0A:
			pos.Set("interval", buf.ReadUnsignedMedium())
			pos.Set("petMode", buf.ReadUnsignedByte())
		case 0x0D:
			pos.Set("passwordProtect", buf.ReadUnsignedInt())
		case 0x0E:
			pos.Set("timeZone", int(buf.ReadSignedByte()))
		case 0x0F:
			pos.Set("enableControl", buf.ReadUnsignedInt())
		case 0x13:
			pos.Set("deviceName", buf.ReadString(length))
		case 0x14:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
		case 0x15:
			pos.Set("bleLatitude", float64(buf.ReadIntLE())/10000000.0)
			pos.Set("bleLongitude", float64(buf.ReadIntLE())/10000000.0)
			pos.Set("bleLocation", buf.ReadString(length-8))
		case 0x17:
			pos.Set("gpsUrl", buf.ReadString(length))
		case 0x18:
			pos.Set("lbsUrl", buf.ReadString(length))
		case 0x1A:
			pos.Set("firmware", buf.ReadString(length))
		case 0x1B:
			pos.Set("gsmModule", buf.ReadString(length))
		case 0x1D:
			pos.Set("agpsUpdate", buf.ReadUnsignedByte())
			pos.Set("agpsLatitude", float64(buf.ReadIntLE())/10000000.0)
			pos.Set("agpsLongitude", float64(buf.ReadIntLE())/10000000.0)
		case 0x30:
			numberIndex := kit.ToInt(buf.ReadUnsignedByte(), 4)
			pos.Set("number"+strconv.Itoa(numberIndex), buf.ReadString(length-1))
		case 0x31:
			pos.Set("prefixFlag", buf.ReadUnsignedByte())
			pos.Set("prefix", buf.ReadString(length-1))
		case 0x33:
			pos.Set("phoneSwitches", buf.ReadUnsignedByte())
		case 0x40:
			pos.Set("apn", buf.ReadString(length))
		case 0x41:
			pos.Set("apnUser", buf.ReadString(length))
		case 0x42:
			pos.Set("apnPassword", buf.ReadString(length))
		case 0x43:
			buf.ReadUnsignedByte() // flag
			pos.Set("port", buf.ReadUnsignedShort())
			pos.Set("server", buf.ReadString(length-3))
		case 0x44:
			pos.Set("heartbeatInterval", buf.ReadUnsignedInt())
			pos.Set("uploadInterval", buf.ReadUnsignedInt())
			pos.Set("uploadLazyInterval", buf.ReadUnsignedInt())
		case 0x47:
			pos.Set("deviceId", buf.ReadString(length))
		case 0x4E:
			pos.Set("gsmBand", buf.ReadUnsignedByte())
		case 0x50:
			pos.Set("powerAlert", buf.ReadUnsignedInt())
		case 0x51:
			pos.Set("geoAlert", buf.ReadUnsignedInt())
		case 0x53:
			pos.Set("motionAlert", buf.ReadUnsignedInt())
		case 0x5C:
			pos.Set("barkLevel", buf.ReadUnsignedByte())
			pos.Set("barkInterval", buf.ReadUnsignedInt())
		case 0x61:
			pos.Set("msisdn", buf.ReadString(length))
		case 0x62:
			pos.Set("wifiWhitelist", buf.ReadUnsignedByte())
			pos.Set("wifiWhitelistMac", buf.ReadSlice(6).HexDump())
		case 0x64:
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set("networkBand", buf.ReadUnsignedInt())
			pos.Set(model.KeyOperator, buf.ReadString(length-5))
		case 0x65:
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set("networkStatus", buf.ReadUnsignedByte())
			pos.Set("serverStatus", buf.ReadUnsignedByte())
			pos.Set("networkPlmn", buf.ReadSlice(6).HexDump())
			pos.Set("homePlmn", buf.ReadSlice(6).HexDump())
		case 0x66:
			pos.Set("imsi", buf.ReadString(length))
		case 0x75:
			pos.Set("extraEnableControl", buf.ReadUnsignedInt())
		}

		buf.SetReaderIndex(end)
	}
	return pos
}

// Encoder is Minifinder2ProtocolEncoder; ModelOverride is setModelOverride's model.
type Encoder struct {
	ModelOverride *string
}

func (e *Encoder) deviceModel(m *ingest.Message, deviceID int64) *string {
	device, err := m.Device(deviceID)
	if err != nil {
		panic(err)
	}
	if device == nil {
		kit.Throw("NullPointerException: device %d", deviceID)
	}
	if e.ModelOverride != nil {
		return e.ModelOverride
	}
	return device.Model
}

func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	if c.Type == model.CommandConfiguration {
		content := kit.Empty().WriteByte8(msgConfiguration)
		if c.Attributes.Has(model.CommandKeyData) {
			data := kit.CommandString(c, model.CommandKeyData)
			if data == nil {
				kit.Throw("NullPointerException: data")
			}
			content.WriteBytes(kit.ParseHex(*data))
		} else {
			content.WriteByte8(1)    // length
			content.WriteByte8(0xF0) // type
		}
		return frame(1, content.Bytes())
	}
	if deviceModel := e.deviceModel(m, c.DeviceID); deviceModel != nil && strings.EqualFold(*deviceModel, "Nano") {
		if c.Type == model.CommandFirmwareUpdate {
			url := kit.CommandString(c, model.CommandKeyData)
			if url == nil {
				kit.Throw("NullPointerException: url")
			}
			content := kit.Empty()
			content.WriteByte8(msgSystemControl)
			content.WriteByte8(1 + kit.Len(*url))
			content.WriteByte8(0x30) // type
			content.WriteASCII(*url)
			return frame(1, content.Bytes())
		}
	}
	return nil
}
