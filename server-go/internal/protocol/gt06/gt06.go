// Package gt06 is the GT06 protocol (Gt06Protocol): Concox/Jimi binary messages framed by
// 0x7878 (one-byte length) or 0x7979 (two-byte length) and ending with "\r\n", with many
// device variants.
package gt06

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "gt06",
		DataCommands: protocol.DataCommands("gt06"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: &Decoder{},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Framer is Gt06FrameDecoder: the length when it ends with "\r\n", else up to the first
// "\r\n".
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 5 {
			return nil
		}
		length := 2 + 2 // head and tail
		if buf.GetByte(buf.ReaderIndex()) == 0x78 {
			length += 1 + buf.GetUnsignedByte(buf.ReaderIndex()+2)
		} else {
			length += 2 + buf.GetUnsignedShort(buf.ReaderIndex()+2)
		}
		if buf.ReadableBytes() >= length && buf.GetUnsignedShort(buf.ReaderIndex()+length-2) == 0x0d0a {
			return buf.ReadSlice(length)
		}
		endIndex := buf.ReaderIndex() - 1
		for {
			endIndex = buf.IndexOf(endIndex+1, buf.WriterIndex(), 0x0d)
			if endIndex > 0 && buf.WriterIndex() > endIndex+1 && buf.GetByte(endIndex+1) == 0x0a {
				return buf.ReadSlice(endIndex + 2 - buf.ReaderIndex())
			}
			if endIndex <= 0 {
				return nil
			}
		}
	}}
}

// Message types.
const (
	msgLogin            = 0x01
	msgGps              = 0x10
	msgGpsLbs6          = 0x11
	msgGpsLbs1          = 0x12
	msgGpsLbs2          = 0x22
	msgGpsLbsDriver     = 0x25 // XT40
	msgGpsLbs3          = 0x37
	msgGpsLbs4          = 0x2D
	msgStatus           = 0x13
	msgString           = 0x15
	msgGpsLbsStatus1    = 0x16
	msgWifi             = 0x17
	msgGpsLbsRfid       = 0x17
	msgGpsLbsStatus2    = 0x26
	msgGpsLbsStatus3    = 0x27
	msgLbsMultiple1     = 0x28
	msgLbsMultiple2     = 0x2E
	msgLbsMultiple3     = 0x24
	msgLbsWifi          = 0x2C
	msgLbsExtend        = 0x18
	msgLbsStatus        = 0x19
	msgGpsPhone         = 0x1A
	msgGpsLbsExtend     = 0x1E // JI09
	msgHeartbeat        = 0x23 // GK310
	msgAddressRequest   = 0x2A // GK310
	msgAddressResponse  = 0x97 // GK310
	msgGpsLbs5          = 0x31 // AZ735 & SL4X
	msgGpsLbsStatus4    = 0x32 // AZ735 & SL4X
	msgWifi5            = 0x33 // AZ735 & SL4X
	msgLbs3             = 0x34 // SL4X
	msgAz735Gps         = 0x32 // AZ735 (extended)
	msgAz735Alarm       = 0x33 // AZ735 (only extended)
	msgX1Gps            = 0x34
	msgX1PhotoInfo      = 0x35
	msgX1PhotoData      = 0x36
	msgLocationRfid     = 0x36 // TRX16i
	msgStatus2          = 0x36 // Jimi IoT 4G
	msgAlarmModule      = 0x39 // Jimi IoT 4G
	msgWifiAlarm        = 0xA9 // Jimi IoT 4G
	msgBluetoothClock   = 0xB2 // Jimi IoT 4G
	msgDeviceStatus     = 0xF1 // Jimi IoT 4G
	msgWifi2            = 0x69
	msgGpsModular       = 0x70
	msgWifi4            = 0xF3
	msgCommand0         = 0x80
	msgCommand1         = 0x81
	msgCommand2         = 0x82
	msgTimeRequest      = 0x8A // GK310
	msgInfo             = 0x94
	msgSerial           = 0x9B
	msgStringInfo       = 0x21
	msgGpsLbs7          = 0xA0 // GK310 & JM-VL03
	msgLbs2             = 0xA1 // GK310
	msgWifi3            = 0xA2 // GK310
	msgGpsLbsStatus5    = 0xA2 // LWxG
	msgFenceMulti       = 0xA4 // GK310 & JM-LL301
	msgLbsAlarm         = 0xA5 // GK310 & JM-LL301
	msgLbsAddress       = 0xA7 // GK310
	msgObd              = 0x8C // FM08ABC
	msgBms              = 0x40 // WD-209
	msgMultimedia       = 0x41 // WD-209
	msgAlarm            = 0x95 // JC100
	msgPeripheral       = 0xF2 // VL842
	msgStatus3OrFence   = 0xA3 // MSG_FENCE_SINGLE (GK310) / MSG_STATUS_3 (GL21L)
	msgGpsLbs8          = 0x38
	msgIButton          = 0x61
	keyTimezone         = "timezone" // DeviceSession.KEY_TIMEZONE
	decoderTimezoneKey  = "decoder.timezone"
	defaultDevicePasswd = "123456"
)

var (
	ntModels = map[string]bool{"NT11": true, "NT20": true, "NT26": true, "NT40": true, "NT46": true, "VL100": true, "XT40": true}
	vlModels = map[string]bool{"VL103": true, "LL303": true, "VL512": true, "G18": true}
)

type variant int

const (
	standard variant = iota
	vxt01
	wanwayS20
	sr411Mini
	gt06eCard
	benway
	s5
	space10x
	obd6
	wetrust
	jc400
	sl4x
	seeworld
	rfid
	lw4g
	trx16i
)

// Decoder is Gt06ProtocolDecoder. ModelOverride is BaseProtocolDecoder.setModelOverride
// (nil: the device's model).
type Decoder struct {
	ModelOverride *string

	variant variant
	media   *mediaBuffer
}

var patternFuel = kit.NewPattern().
	Text("!AIOIL,").
	Number("d+,").        // device address
	Number("d+.d+,").     // output value
	Number("(d+.d+),").   // temperature
	Expression("[^,]+,"). // version
	Number("dd").         // back wave
	Number("d").          // software status code
	Number("d,").         // hardware status code
	Number("(d+.d+),").   // measured value
	Expression("[01],").  // movement status
	Number("d+,").        // excited wave times
	Number("xx").         // checksum
	Compile()

var patternLocation = kit.NewPattern().
	Text("Current position!").
	Number("Lat:([NS])(d+.d+),").       // latitude
	Number("Lon:([EW])(d+.d+),").       // longitude
	Text("Course:").Number("(d+.d+),"). // course
	Text("Speed:").Number("(d+.d+),").  // speed
	Text("DateTime:").
	Number("(dddd)-(dd)-(dd) +"). // date
	Number("(dd):(dd):(dd)").     // time
	Compile()

func (d *Decoder) isSupported(t int, model *string) bool {
	return d.hasGps(t) || d.hasLbs(t) || d.hasStatus(t, model)
}

func (d *Decoder) hasGps(t int) bool {
	switch t {
	case msgGps, msgGpsLbs1, msgGpsLbs2, msgGpsLbsDriver, msgGpsLbs3, msgGpsLbs4, msgGpsLbs5, msgGpsLbs6,
		msgGpsLbsStatus1, msgGpsLbsStatus2, msgGpsLbsStatus3, msgGpsLbsStatus4, msgGpsLbsStatus5,
		msgGpsPhone, msgGpsLbsExtend, msgGpsLbs7, msgGpsLbsRfid, msgFenceMulti, msgAlarmModule:
		return true
	case msgStatus3OrFence:
		return d.variant != seeworld
	}
	return false
}

func (d *Decoder) hasLbs(t int) bool {
	switch t {
	case msgLbsStatus, msgGpsLbs1, msgGpsLbs2, msgGpsLbsDriver, msgGpsLbs3, msgGpsLbs4, msgGpsLbs5, msgGpsLbs6,
		msgGpsLbsStatus1, msgGpsLbsStatus2, msgGpsLbsStatus3, msgGpsLbsStatus4, msgGpsLbsStatus5,
		msgGpsLbs7, msgGpsLbsRfid, msgFenceMulti, msgAlarmModule, msgLbsAlarm, msgLbsAddress:
		return true
	case msgStatus3OrFence:
		return d.variant != seeworld
	}
	return false
}

func (d *Decoder) hasStatus(t int, model *string) bool {
	switch t {
	case msgStatus, msgStatus2, msgLbsStatus, msgGpsLbsStatus1, msgGpsLbsStatus2, msgGpsLbsStatus3,
		msgGpsLbsStatus4, msgGpsLbsStatus5, msgFenceMulti, msgAlarmModule, msgLbsAlarm:
		return true
	case msgGpsLbs2, msgGpsLbsDriver:
		return model != nil && ntModels[strings.ToUpper(*model)]
	case msgStatus3OrFence:
		return d.variant == seeworld
	}
	return false
}

// sendResponse is Gt06ProtocolDecoder.sendResponse (content may be nil).
func sendResponse(m *ingest.Message, extended bool, t, index int, content []byte) {
	response := kit.Empty()
	length := 5 + len(content)
	if extended {
		response.WriteShort(0x7979)
		response.WriteShort(length)
	} else {
		response.WriteShort(0x7878)
		response.WriteByte8(length)
	}
	response.WriteByte8(t)
	response.WriteBytes(content)
	response.WriteShort(index)
	response.WriteShort(kit.CRC16(kit.CRC16_X25, response.Array()[2:response.WriterIndex()]))
	response.WriteByte8('\r')
	response.WriteByte8('\n')
	m.Send(response)
}

func (d *Decoder) sendPhotoRequest(m *ingest.Message, pictureID int32) {
	photo := d.media
	content := kit.Empty()
	content.WriteInt(int64(pictureID))
	content.WriteInt(int64(photo.writerIndex()))
	content.WriteShort(min(photo.writableBytes(), 1024))
	sendResponse(m, false, msgX1PhotoData, 0, content.Bytes())
}

// DecodeGps is Gt06ProtocolDecoder.decodeGps(position, buf, hasLength, timezone), also
// used by other protocols.
func DecodeGps(pos *model.Position, buf *kit.Buf, hasLength bool, zone *time.Location) bool {
	return decodeGps(pos, buf, hasLength, true, true, false, false, zone)
}

func decodeGps(pos *model.Position, buf *kit.Buf, hasLength, hasSatellites, hasSpeed, longSpeed, swapFlags bool,
	zone *time.Location) bool {

	b := kit.NewDateBuilder(zone)
	b.SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	b.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	pos.SetTime(b.Date())

	if hasLength && buf.ReadUnsignedByte() == 0 {
		return false
	}
	if hasSatellites {
		pos.Set(model.KeySatellites, kit.ToInt(buf.ReadUnsignedByte(), 4))
	}

	latitude := float64(buf.ReadUnsignedInt()) / 60.0 / 30000.0
	longitude := float64(buf.ReadUnsignedInt()) / 60.0 / 30000.0

	flags := 0
	if swapFlags {
		flags = buf.ReadUnsignedShort()
	}
	if hasSpeed {
		if longSpeed {
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
		} else {
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		}
	}
	if !swapFlags {
		flags = buf.ReadUnsignedShort()
	}

	f := int64(flags)
	pos.Course = float64(kit.ToInt(flags, 10))
	pos.Valid = kit.Check(f, 12)
	if !kit.Check(f, 10) {
		latitude = -latitude
	}
	if kit.Check(f, 11) {
		longitude = -longitude
	}
	pos.SetLatitude(latitude)
	pos.SetLongitude(longitude)

	if kit.Check(f, 14) {
		pos.Set(model.KeyIgnition, kit.Check(f, 15))
	}
	return true
}

func (d *Decoder) decodeLbs(pos *model.Position, buf *kit.Buf, t int, hasLength bool) bool {
	length := 0
	if hasLength {
		length = buf.ReadUnsignedByte()
		if length == 0 {
			zeroedData := true
			for i := buf.ReaderIndex() + 9; i < buf.ReaderIndex()+45 && i < buf.WriterIndex(); i++ {
				if buf.GetByte(i) != 0 {
					zeroedData = false
					break
				}
			}
			if zeroedData {
				buf.Skip(min(buf.ReadableBytes(), 45))
			}
			return false
		}
	}

	cellType := 0
	if t == msgGpsLbs8 || t == msgAlarmModule {
		cellType = buf.ReadUnsignedByte()
	}

	mcc := buf.ReadUnsignedShort()
	var mnc int
	if kit.Check(int64(mcc), 15) || t == msgGpsLbs6 || d.variant == sl4x {
		mnc = buf.ReadUnsignedShort()
	} else {
		mnc = buf.ReadUnsignedByte()
	}
	var lac int
	if cellType >= 3 || t == msgLbsAlarm || t == msgGpsLbs7 || t == msgGpsLbsStatus5 {
		lac = int(buf.ReadInt())
	} else {
		lac = buf.ReadUnsignedShort()
	}
	var cid int64
	switch {
	case cellType >= 3 || t == msgLbsAlarm || t == msgGpsLbs7 || d.variant == sl4x || t == msgGpsLbsStatus5:
		cid = buf.ReadLong()
	case t == msgGpsLbs6 || t == msgIButton || t == msgGpsLbsDriver || d.variant == seeworld ||
		(d.variant == trx16i && t == msgLocationRfid):
		cid = buf.ReadUnsignedInt()
	default:
		cid = int64(buf.ReadUnsignedMedium())
	}

	if cellType >= 3 {
		buf.ReadUnsignedShort() // rssi
	} else if t == msgGpsLbs8 || t == msgAlarmModule {
		buf.ReadUnsignedByte() // rssi
	}

	pos.Network = model.NetworkOf(model.NewCellTower(kit.ToInt(mcc, 15), mnc, lac, cid))

	if length > 9 {
		buf.Skip(length - 9)
	}
	return true
}

func (d *Decoder) decodeStatus(pos *model.Position, buf *kit.Buf) {
	status := buf.ReadUnsignedByte()
	s := int64(status)

	pos.Set(model.KeyStatus, status)
	pos.Set(model.KeyIgnition, kit.Check(s, 1))
	pos.Set(model.KeyCharge, kit.Check(s, 2))
	pos.Set(model.KeyBlocked, kit.Check(s, 7))

	switch kit.BetweenInt(status, 3, 6) {
	case 1:
		pos.AddAlarm(model.AlarmVibration)
	case 2:
		pos.AddAlarm(model.AlarmPowerCut)
	case 3:
		pos.AddAlarm(model.AlarmLowBattery)
	case 4:
		pos.AddAlarm(model.AlarmSos)
	case 6:
		pos.AddAlarm(model.AlarmGeofence)
	case 7:
		if d.variant == vxt01 {
			pos.AddAlarm(model.AlarmOverspeed)
		} else {
			pos.AddAlarm(model.AlarmRemoving)
		}
	}
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (d *Decoder) decodeAlarm(value int, modelLW, modelSW, modelVL bool) string {
	switch value {
	case 0x01:
		return model.AlarmSos
	case 0x02:
		return model.AlarmPowerCut
	case 0x03:
		return model.AlarmVibration
	case 0x04:
		return model.AlarmGeofenceEnter
	case 0x05:
		return model.AlarmGeofenceExit
	case 0x06:
		return model.AlarmOverspeed
	case 0x09:
		if d.variant == trx16i {
			return model.AlarmGeofence
		}
		return pick(modelVL, model.AlarmTow, model.AlarmVibration)
	case 0x0E:
		return pick(d.variant == trx16i, model.AlarmLowPower, model.AlarmLowBattery)
	case 0x0F:
		return model.AlarmLowBattery
	case 0x11:
		return model.AlarmPowerOff
	case 0x0C, 0x13, 0x25, 0x32:
		return model.AlarmTampering
	case 0x14:
		return model.AlarmDoor
	case 0x18:
		return pick(modelLW, model.AlarmAccident, model.AlarmRemoving)
	case 0x19:
		return pick(modelLW, model.AlarmAcceleration, model.AlarmLowBattery)
	case 0x1A, 0x27, 0xF1:
		return model.AlarmBraking
	case 0x1B, 0x2A, 0x2B, 0x2E:
		return model.AlarmCornering
	case 0x23:
		return model.AlarmFallDown
	case 0x26, 0xF0:
		return model.AlarmAcceleration
	case 0x28:
		if modelSW {
			return model.AlarmCornering
		}
		return pick(modelVL, model.AlarmPowerOff, model.AlarmBraking)
	case 0x29:
		return pick(modelSW, model.AlarmAccident, model.AlarmAcceleration)
	case 0x2C, 0xF2:
		return model.AlarmAccident
	case 0x30:
		return pick(modelVL, model.AlarmBraking, model.AlarmJamming)
	case 0x33:
		return model.AlarmLock
	case 0x34:
		return model.AlarmUnlock
	case 0x53:
		return model.AlarmFuelLeak
	case 0x5B, 0x5C:
		return model.AlarmTemperature
	case 0xC9:
		return model.AlarmIdle
	case 0x0107, 0x010B:
		return model.AlarmJamming
	}
	return ""
}

// timeZone is getTimeZone(deviceId, defaultTimeZone): nil when neither decoder.timezone
// nor a default is set ("" is Java's null default).
func timeZone(m *ingest.Message, deviceID int64, def string) *time.Location {
	name, err := m.LookupString(deviceID, decoderTimezoneKey, false, true)
	if err != nil {
		panic(err)
	}
	switch {
	case name != nil:
		return javatime.TimeZone(*name)
	case def != "":
		return javatime.TimeZone(def)
	}
	return nil
}

func sessionZone(ds *ingest.DeviceSession) *time.Location {
	zone, _ := ds.Get(keyTimezone).(*time.Location)
	if zone == nil {
		kit.Throw("NullPointerException: timezone")
	}
	return zone
}

func decodeDate(buf *kit.Buf, ds *ingest.DeviceSession) time.Time {
	b := kit.NewDateBuilder(sessionZone(ds))
	b.SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	b.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	return b.Date()
}

func lastLocation(m *ingest.Message, pos *model.Position, t time.Time) {
	m.LastLocation(pos, &t)
}

// mac is a hex dump with ':' after every two digits, the last one dropped.
func mac(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02x", v)
	}
	return strings.Join(parts, ":")
}

func allZeros(s string) bool { // s.matches("0+")
	return s != "" && strings.Trim(s, "0") == ""
}

// utf16BE is new String(bytes, UTF_16BE): malformed input is U+FFFD.
func utf16BE(b []byte) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	s := string(utf16.Decode(units))
	if len(b)%2 != 0 {
		s += "�"
	}
	return s
}

func (d *Decoder) decodeVariant(buf *kit.Buf) {
	header := buf.GetUnsignedShort(buf.ReaderIndex())
	var length, t int
	if header == 0x7878 {
		length = buf.GetUnsignedByte(buf.ReaderIndex() + 2)
		t = buf.GetUnsignedByte(buf.ReaderIndex() + 2 + 1)
	} else {
		length = buf.GetUnsignedShort(buf.ReaderIndex() + 2)
		t = buf.GetUnsignedByte(buf.ReaderIndex() + 2 + 2)
	}
	short := header == 0x7878
	switch {
	case short && t == msgGpsLbs1 && length == 0x24:
		d.variant = vxt01
	case short && t == msgGpsLbsStatus1 && length == 0x24:
		d.variant = vxt01
	case short && t == msgLbsMultiple3 && length == 0x31:
		d.variant = wanwayS20
	case short && t == msgLbsMultiple3 && length == 0x2e:
		d.variant = sr411Mini
	case short && t == msgGpsLbs1 && length >= 0x71:
		d.variant = gt06eCard
	case short && t == msgGpsLbs1 && length == 0x21:
		d.variant = benway
	case short && t == msgGpsLbs1 && length == 0x2b:
		d.variant = s5
	case short && t == msgLbsStatus && length >= 0x17:
		d.variant = space10x
	case short && t == msgStatus && length == 0x13:
		d.variant = obd6
	case short && t == msgGpsLbs1 && length == 0x29:
		d.variant = wetrust
	case short && t == msgAlarm && buf.GetUnsignedShort(buf.ReaderIndex()+4) == 0xffff:
		d.variant = jc400
	case short && t == msgLbs3 && length == 0x37:
		d.variant = sl4x
	case short && t == msgGpsLbs5 && length == 0x2a:
		d.variant = sl4x
	case short && t == msgGpsLbsStatus4 && length == 0x27:
		d.variant = sl4x
	case short && t == msgGpsLbsStatus4 && length == 0x29:
		d.variant = sl4x
	case short && t == msgGpsLbs2 && length == 0x2f:
		d.variant = seeworld
	case short && t == msgGpsLbsStatus1 && length == 0x26:
		d.variant = seeworld
	case short && t == msgStatus3OrFence && length == 0x0c:
		d.variant = seeworld
	case short && t == msgStatus3OrFence && length == 0x18:
		d.variant = seeworld
	case short && t == msgGpsLbsRfid && length == 0x28:
		d.variant = rfid
	case short && t == msgGpsLbsStatus5 && length == 0x40:
		d.variant = lw4g
	case short && t == msgLocationRfid && length == 0x35:
		d.variant = trx16i
	case short && t == msgGpsLbsStatus1 && length == 0x2D:
		d.variant = trx16i
	default:
		d.variant = standard
	}
}

// Decode is Gt06ProtocolDecoder.decode.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	d.decodeVariant(buf)

	extended := buf.ReadShort() != 0x7878
	var length int
	if extended {
		length = buf.ReadUnsignedShort()
	} else {
		length = buf.ReadUnsignedByte()
	}
	dataLength := length - 5
	t := buf.ReadUnsignedByte()

	pos := m.NewPosition()
	var ds *ingest.DeviceSession
	if t != msgLogin {
		ds = m.Session()
		if ds == nil {
			return nil
		}
		pos.DeviceID = ds.DeviceID
		if ds.Get(keyTimezone) == nil {
			ds.Set(keyTimezone, timeZone(m, ds.DeviceID, "UTC"))
		}
	}

	var modelName *string
	if ds != nil {
		if d.ModelOverride != nil {
			modelName = d.ModelOverride
		} else {
			modelName = ds.Model
		}
	}
	if modelName != nil {
		upper := strings.ToUpper(*modelName)
		modelName = &upper
	}
	modelLW := modelName != nil && strings.HasPrefix(*modelName, "LW")
	modelSW := modelName != nil && *modelName == "SEEWORLD"
	modelNT := modelName != nil && ntModels[*modelName]
	modelVL := modelName != nil && vlModels[*modelName]
	modelIs := func(name string) bool { return modelName != nil && *modelName == name }

	index := func() int { return buf.GetShort(buf.WriterIndex() - 6) }

	switch {
	case t == msgLogin:
		imei := buf.ReadSlice(8).HexDump()[1:]
		buf.ReadUnsignedShort() // type

		ds = m.Session(imei)
		if ds != nil {
			zone := timeZone(m, ds.DeviceID, "")
			if zone == nil && dataLength > 10 {
				extensionBits := buf.ReadUnsignedShort()
				hours := (extensionBits >> 4) / 100
				minutes := (extensionBits >> 4) % 100
				offset := (hours*60 + minutes) * 60
				if extensionBits&0x8 != 0 {
					offset = -offset
				}
				zone = time.FixedZone("UTC", offset)
			}
			if zone == nil {
				ds.Set(keyTimezone, nil)
			} else {
				ds.Set(keyTimezone, zone)
			}
			sendResponse(m, false, t, index(), nil)
		}
		return nil

	case t == msgHeartbeat:
		m.LastLocation(pos, nil)

		status := int64(buf.ReadUnsignedByte())
		pos.Set(model.KeyArmed, kit.Check(status, 0))
		pos.Set(model.KeyIgnition, kit.Check(status, 1))
		pos.Set(model.KeyCharge, kit.Check(status, 2))

		if buf.ReadableBytes() >= 2+6 {
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		}
		if buf.ReadableBytes() >= 1+6 {
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		}

		sendResponse(m, false, t, index(), nil)
		return pos

	case t == msgAddressRequest:
		response := "NA&&NA&&0##"
		content := kit.Empty()
		content.WriteByte8(len(response))
		content.WriteInt(0)
		content.WriteASCII(response)
		sendResponse(m, true, msgAddressResponse, 0, content.Bytes())
		return nil

	case t == msgTimeRequest:
		now := m.Now.UTC()
		content := kit.Empty()
		content.WriteByte8(now.Year() - 2000)
		content.WriteByte8(int(now.Month()))
		content.WriteByte8(now.Day())
		content.WriteByte8(now.Hour())
		content.WriteByte8(now.Minute())
		content.WriteByte8(now.Second())
		sendResponse(m, false, msgTimeRequest, 0, content.Bytes())
		return nil

	case t == msgX1Gps && d.variant != sl4x:
		buf.ReadUnsignedInt() // data and alarm

		DecodeGps(pos, buf, false, sessionZone(ds))

		buf.ReadUnsignedShort() // terminal info

		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())

		pos.Network = model.NetworkOf(model.NewCellTower(
			buf.ReadUnsignedShort(), buf.ReadUnsignedByte(), buf.ReadUnsignedShort(), buf.ReadUnsignedInt()))

		driverID := buf.ReadUnsignedInt()
		if driverID > 0 {
			pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(driverID, 10))
		}

		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)

		portInfo := buf.ReadUnsignedInt()

		pos.Set(model.KeyInput, buf.ReadUnsignedByte())
		pos.Set(model.KeyOutput, buf.ReadUnsignedByte())

		for i := int64(1); i <= kit.Between(portInfo, 20, 24); i++ {
			pos.Set(model.PrefixAdc+strconv.FormatInt(i, 10), float64(buf.ReadUnsignedShort())/100.0)
		}
		return pos

	case t == msgX1PhotoInfo:
		buf.Skip(6)            // time
		buf.ReadUnsignedByte() // fix status
		buf.ReadUnsignedInt()  // latitude
		buf.ReadUnsignedInt()  // longitude
		buf.ReadUnsignedByte() // camera id
		buf.ReadUnsignedByte() // photo source
		buf.ReadUnsignedByte() // picture format
		d.media = nil          // released before the new buffer is made
		d.media = newMediaBuffer(m, int(buf.ReadInt()))
		pictureID := buf.ReadInt()
		d.sendPhotoRequest(m, pictureID)
		return nil

	case (t == msgWifi && d.variant != rfid) || t == msgWifi2 || t == msgWifi4:
		timeBytes := buf.GetBytes(buf.ReaderIndex(), 6)
		timeBuf := buf.ReadSlice(6)
		b := kit.NewDateBuilder(nil)
		b.SetYear(kit.ReadBCD(timeBuf, 2))
		b.SetMonth(kit.ReadBCD(timeBuf, 2))
		b.SetDay(kit.ReadBCD(timeBuf, 2))
		b.SetHour(kit.ReadBCD(timeBuf, 2))
		b.SetMinute(kit.ReadBCD(timeBuf, 2))
		b.SetSecond(kit.ReadBCD(timeBuf, 2))
		lastLocation(m, pos, b.Date())

		network := model.NewNetwork()

		var wifiCount int
		if t == msgWifi4 {
			wifiCount = buf.ReadUnsignedByte()
		} else {
			wifiCount = buf.GetUnsignedByte(2)
		}
		for range wifiCount {
			if t == msgWifi4 {
				buf.Skip(2)
			}
			address := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
				buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte(),
				buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			w := model.WifiAccessPoint{MacAddress: &address}
			if t != msgWifi4 {
				w.SetSignalStrength(buf.ReadUnsignedByte())
			}
			network.AddWifiAccessPoint(w)
		}

		if t != msgWifi4 {
			cellCount := buf.ReadUnsignedByte()
			mcc := buf.ReadUnsignedShort()
			mnc := buf.ReadUnsignedByte()
			for range cellCount {
				network.AddCellTower(model.NewCellTowerRSSI(
					mcc, mnc, buf.ReadUnsignedShort(), int64(buf.ReadUnsignedShort()), buf.ReadUnsignedByte()))
			}

			response := kit.Empty()
			response.WriteShort(0x7878)
			response.WriteByte8(0)
			response.WriteByte8(t)
			response.WriteBytes(timeBytes)
			response.WriteByte8('\r')
			response.WriteByte8('\n')
			m.Send(response)
		}

		pos.Network = network
		return pos

	case t == msgInfo && !extended:
		m.LastLocation(pos, nil)
		pos.Set(model.KeyPower, float64(buf.ReadShort())/100.0)
		return pos

	case t == msgLbsMultiple3 && d.variant == sr411Mini:
		DecodeGps(pos, buf, false, sessionZone(ds))
		d.decodeLbs(pos, buf, t, false)
		pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		return pos

	case t == msgLbsMultiple1 || t == msgLbsMultiple2 || t == msgLbsMultiple3 ||
		t == msgLbsExtend || t == msgLbsWifi || t == msgLbs2 || t == msgLbs3 ||
		(t == msgWifi3 && d.variant != lw4g) || (t == msgWifi5 && !extended) ||
		t == msgWifiAlarm:

		lastLocation(m, pos, decodeDate(buf, ds))

		if d.variant == wanwayS20 || d.variant == sl4x {
			buf.ReadUnsignedByte() // ta
		}

		mcc := buf.ReadUnsignedShort()
		var mnc int
		if kit.Check(int64(mcc), 15) || d.variant == sl4x {
			mnc = buf.ReadUnsignedShort()
		} else {
			mnc = buf.ReadUnsignedByte()
		}

		network := model.NewNetwork()

		cell4g := t == msgWifiAlarm && buf.ReadUnsignedByte() > 0
		var cellCount int
		switch {
		case d.variant == wanwayS20 || t == msgWifiAlarm:
			cellCount = buf.ReadUnsignedByte()
		case t == msgWifi5:
			cellCount = 6
		default:
			cellCount = 7
		}
		for range cellCount {
			var lac, cid int
			switch {
			case t == msgLbs2 || t == msgWifi3 || cell4g:
				lac = int(buf.ReadInt())
				cid = int(int32(buf.ReadLong()))
			case t == msgWifi5 || t == msgLbs3:
				lac = buf.ReadUnsignedShort()
				cid = int(int32(buf.ReadUnsignedInt()))
			default:
				lac = buf.ReadUnsignedShort()
				cid = buf.ReadUnsignedMedium()
			}
			rssi := -buf.ReadUnsignedByte()
			if lac > 0 {
				network.AddCellTower(model.NewCellTowerRSSI(kit.ToInt(mcc, 15), mnc, lac, int64(cid), rssi))
			}
		}

		if d.variant != wanwayS20 && d.variant != sl4x {
			buf.ReadUnsignedByte() // ta
		}

		if t != msgLbsMultiple1 && t != msgLbsMultiple2 && t != msgLbsMultiple3 && t != msgLbs2 && t != msgLbs3 {
			wifiCount := buf.ReadUnsignedByte()
			for range wifiCount {
				address := mac(buf.ReadBytes(6))
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(address, buf.ReadUnsignedByte()))
			}
		}

		pos.Network = network

		if t == msgWifiAlarm {
			alarm := buf.ReadUnsignedByte()
			language := buf.ReadUnsignedByte()
			pos.AddAlarm(d.decodeAlarm((language>>4)<<8|alarm, modelLW, modelSW, modelVL))
		}

	case t == msgBluetoothClock:
		lastLocation(m, pos, decodeDate(buf, ds))
		pos.Set(model.KeyRssi, int(buf.ReadSignedByte()))
		pos.Set("tagMac", buf.ReadSlice(6).HexDump())
		pos.Set("tagUuid", buf.ReadSlice(16).HexDump())
		pos.Set("tagMajor", buf.ReadUnsignedShort())
		pos.Set("tagMinor", buf.ReadUnsignedShort())
		pos.Set("tagBattery", float64(buf.ReadUnsignedShort())/100.0)
		pos.Set("clock", kit.BetweenInt(buf.ReadUnsignedByte(), 2, 6))

	case t == msgString:
		m.LastLocation(pos, nil)

		commandLength := buf.ReadUnsignedByte()
		if commandLength > 0 {
			buf.ReadUnsignedInt() // server flag (reserved)
			data := kit.Latin1(buf.ReadSlice(commandLength - 4).Bytes())
			if strings.HasPrefix(data, "<ICCID:") {
				pos.Set(model.KeyIccid, kit.Substring(data, 7, 27))
			} else {
				pos.Set(model.KeyResult, data)
			}
		}

	case t == msgBms:
		buf.Skip(8) // serial number

		lastLocation(m, pos, time.UnixMilli(buf.ReadUnsignedInt()*1000))

		pos.Set("relativeCapacity", buf.ReadUnsignedByte())
		pos.Set("remainingCapacity", buf.ReadUnsignedShort())
		pos.Set("absoluteCapacity", buf.ReadUnsignedByte())
		pos.Set("fullCapacity", buf.ReadUnsignedShort())
		pos.Set("batteryHealth", buf.ReadUnsignedByte())
		pos.Set("batteryTemp", float64(buf.ReadUnsignedShort())/10.0-273.1)
		pos.Set("current", buf.ReadUnsignedShort())
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
		pos.Set("cycleIndex", buf.ReadUnsignedShort())
		for i := 1; i <= 14; i++ {
			pos.Set("batteryCell"+strconv.Itoa(i), float64(buf.ReadUnsignedShort())/1000.0)
		}
		pos.Set("currentChargeInterval", buf.ReadUnsignedShort())
		pos.Set("maxChargeInterval", buf.ReadUnsignedShort())
		pos.Set("barcode", kit.Trim(buf.ReadString(16)))
		pos.Set("batteryVersion", buf.ReadUnsignedShort())
		pos.Set("manufacturer", kit.Trim(buf.ReadString(16)))
		pos.Set("batteryStatus", buf.ReadUnsignedInt())
		pos.Set("controllerStatus", buf.ReadUnsignedInt())
		pos.Set("controllerFault", buf.ReadUnsignedInt())

		sendResponse(m, false, t, index(), nil)
		return pos

	case t == msgStatus && buf.ReadableBytes() == 22:
		m.LastLocation(pos, nil)

		buf.ReadUnsignedByte()  // information content
		buf.ReadUnsignedShort() // satellites
		buf.ReadUnsignedByte()  // alarm
		buf.ReadUnsignedByte()  // language

		pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())

		buf.ReadUnsignedByte()  // working mode
		buf.ReadUnsignedShort() // working voltage
		buf.ReadUnsignedByte()  // reserved
		buf.ReadUnsignedShort() // working times
		buf.ReadUnsignedShort() // working time

		value := buf.ReadUnsignedShort()
		temperature := float64(kit.ToInt(value, 15)) / 10.0
		if kit.Check(int64(value), 15) {
			pos.Set(model.PrefixTemp+"1", temperature)
		} else {
			pos.Set(model.PrefixTemp+"1", -temperature)
		}

	case t == msgIButton:
		buf.Skip(8) // imei
		DecodeGps(pos, buf, false, sessionZone(ds))
		d.decodeLbs(pos, buf, t, false)
		driverUniqueID := buf.ReadUnsignedInt()<<16 | int64(buf.ReadUnsignedShort())
		pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(driverUniqueID, 10))

	case t == msgLocationRfid && d.variant == trx16i:
		DecodeGps(pos, buf, false, sessionZone(ds))
		d.decodeLbs(pos, buf, t, false)
		pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
		pos.Set("uploadMode", buf.ReadUnsignedByte())
		pos.Set(model.KeyArchive, buf.ReadUnsignedByte() > 0)
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()) // unit unconfirmed
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/10.0)
		pos.Set(model.KeyHours, int64(buf.ReadUnsignedMedium())*60*1000)
		if id := buf.ReadSlice(8).HexDump(); !allZeros(id) {
			pos.Set(model.KeyDriverUniqueID, id)
		}

	case d.isSupported(t, modelName) && !(extended && (t == msgGpsLbsStatus4 || t == msgStatus2)):
		if t == msgLbsStatus && d.variant == space10x {
			return nil // multi-lbs message
		}
		if !d.decodeSupported(m, pos, buf, ds, t, length, modelName, modelLW, modelSW, modelNT, modelVL, modelIs) {
			return nil
		}

	case t == msgAlarm:
		isJC400 := d.variant == jc400
		extendedAlarm := dataLength > 7
		if extendedAlarm {
			if isJC400 {
				buf.ReadUnsignedShort() // marker
				buf.ReadUnsignedByte()  // version
			}
			decodeGps(pos, buf, false, isJC400, isJC400, isJC400, isJC400, sessionZone(ds))
		} else {
			lastLocation(m, pos, decodeDate(buf, ds))
		}
		if d.variant == jc400 {
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
		}
		event := buf.ReadUnsignedByte()
		pos.Set(model.KeyEvent, event)
		pos.Set("eventData", buf.ReadUnsignedShort())
		switch event {
		case 0x01:
			pos.AddAlarm(pick(extendedAlarm, model.AlarmSos, model.AlarmGeneral))
		case 0x0E:
			pos.AddAlarm(model.AlarmLowPower)
		case 0x76:
			pos.AddAlarm(model.AlarmTemperature)
		case 0x80:
			pos.AddAlarm(model.AlarmVibration)
		case 0x87:
			pos.AddAlarm(model.AlarmOverspeed)
		case 0x88:
			pos.AddAlarm(model.AlarmPowerCut)
		case 0x90:
			pos.AddAlarm(model.AlarmAcceleration)
		case 0x91:
			pos.AddAlarm(model.AlarmBraking)
		case 0x92:
			pos.AddAlarm(model.AlarmCornering)
		case 0x93:
			pos.AddAlarm(model.AlarmAccident)
		}
		if filesLength := buf.ReadableBytes() - 6; filesLength > 0 {
			pos.Set("eventFiles", buf.ReadString(filesLength))
		}

	case t == msgStringInfo:
		buf.ReadUnsignedInt() // server flag
		var data string
		if buf.ReadUnsignedByte() == 1 {
			data = kit.Latin1(buf.ReadSlice(buf.ReadableBytes() - 6).Bytes())
		} else {
			data = utf16BE(buf.ReadSlice(buf.ReadableBytes() - 6).Bytes())
		}

		parser := kit.NewParser(patternLocation, data)
		if parser.Matches() {
			pos.Valid = true
			pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
			pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))
			pos.Course = parser.MustDouble()
			pos.Speed = parser.MustDouble()
			pos.SetTime(parser.NextDateTime(kit.YMD_HMS, nil, m.Now))
		} else {
			m.LastLocation(pos, nil)
			pos.Set(model.KeyResult, data)
		}
		return pos

	case t == msgInfo:
		return d.decodeInfo(m, pos, buf, ds, length)

	case t == msgX1PhotoData:
		pictureID := buf.ReadInt()
		photo := d.media
		buf.ReadUnsignedInt() // offset
		if photo == nil {
			kit.Throw("NullPointerException: media buffer")
		}
		photo.readFrom(buf, buf.ReadUnsignedShort())
		if photo.writableBytes() > 0 {
			d.sendPhotoRequest(m, pictureID)
		} else {
			pos.Set(model.KeyImage, d.writeMedia(m, ds))
		}
		return nil

	case t == msgAz735Gps || t == msgAz735Alarm:
		if !DecodeGps(pos, buf, true, sessionZone(ds)) {
			lastLocation(m, pos, pos.DeviceTime.Time)
		}
		if d.decodeLbs(pos, buf, t, true) {
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		}

		buf.Skip(buf.ReadUnsignedByte()) // additional cell towers
		buf.Skip(buf.ReadUnsignedByte()) // wifi access point

		status := buf.ReadUnsignedByte()
		pos.Set(model.KeyStatus, status)

		if t == msgAz735Alarm {
			switch status {
			case 0xA0:
				pos.Set(model.KeyArmed, true)
			case 0xA1:
				pos.Set(model.KeyArmed, false)
			case 0xA2, 0xA3:
				pos.AddAlarm(model.AlarmLowBattery)
			case 0xA4:
				pos.AddAlarm(model.AlarmGeneral)
			case 0xA5:
				pos.AddAlarm(model.AlarmDoor)
			}
		}

		buf.Skip(buf.ReadUnsignedByte()) // reserved extension

		sendResponse(m, true, t, index(), nil)
		return pos

	case t == msgObd:
		lastLocation(m, pos, decodeDate(buf, ds))

		pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)

		data := buf.ReadString(buf.ReadableBytes() - 18)
		for _, pair := range kit.Split(data, ",") {
			values := kit.Split(pair, "=")
			if len(values) >= 2 {
				value := func() float64 { return float64(kit.IntRadix(values[1], 16)) / 100.0 }
				switch kit.IntRadix(kit.Substring(values[0], 0, 2), 16) {
				case 40:
					pos.Set(model.KeyOdometer, value())
				case 43:
					pos.Set(model.KeyFuel, value())
				case 45:
					pos.Set(model.KeyCoolantTemp, value())
				case 53:
					pos.Set(model.KeyObdSpeed, value())
				case 54:
					pos.Set(model.KeyRpm, value())
				case 71:
					pos.Set(model.KeyFuelUsed, value())
				case 73:
					pos.Set(model.KeyHours, value())
				case 74:
					pos.Set(model.KeyVin, values[1])
				}
			}
		}
		return pos

	case t == msgGpsModular:
		d.decodeModular(m, pos, buf)
		if pos.FixTime.IsZero() {
			m.LastLocation(pos, nil)
		}
		sendResponse(m, false, msgGpsModular, buf.ReadUnsignedShort(), nil)
		return pos

	case t == msgMultimedia:
		buf.Skip(8) // serial number
		timestamp := buf.ReadUnsignedInt() * 1000
		buf.Skip(4 + 4 + 2 + 1 + 1 + 2) // gps
		buf.Skip(2 + 2 + 2 + 2)         // cell
		buf.ReadInt()                   // media id
		mediaLength := int(buf.ReadInt())
		mediaType := buf.ReadUnsignedByte()
		mediaFormat := buf.ReadUnsignedByte()
		if mediaType == 0 && mediaFormat == 0 {
			buf.ReadUnsignedByte() // event
			var photo *mediaBuffer
			if buf.ReadUnsignedShort() == 0 {
				d.media = nil
				d.media = newMediaBuffer(m, mediaLength)
			}
			photo = d.media
			if photo != nil {
				photo.readFrom(buf, buf.ReadableBytes()-3*2)
				if photo.writableBytes() <= 0 {
					lastLocation(m, pos, time.UnixMilli(timestamp))
					pos.Set(model.KeyImage, d.writeMedia(m, ds))
				}
			}
		}
		sendResponse(m, true, t, index(), nil)
		return pos

	case t == msgSerial:
		m.LastLocation(pos, nil)
		buf.ReadUnsignedByte()                         // external device type code
		data := buf.ReadSlice(buf.ReadableBytes() - 6) // index + checksum + footer
		if kit.IsPrintable(data, data.ReadableBytes()) {
			pos.Set(model.KeyResult, kit.Trim(data.ReadString(data.ReadableBytes())))
		} else {
			pos.Set(model.KeyResult, data.HexDump())
		}
		return pos

	case t == msgPeripheral || t == msgDeviceStatus:
		lastLocation(m, pos, time.UnixMilli(buf.ReadUnsignedInt()*1000))
		d.decodePeripheral(pos, buf, t)
		sendResponse(m, false, t, index(), nil)
		return pos

	case t == msgGpsLbs8:
		DecodeGps(pos, buf, false, sessionZone(ds))
		buf.ReadUnsignedByte() // data upload mode
		buf.ReadUnsignedByte() // re-upload
		d.decodeLbs(pos, buf, t, false)
		return pos

	default:
		if dataLength > 0 {
			buf.Skip(dataLength)
		}
		if !extended && t != msgCommand0 && t != msgCommand1 && t != msgCommand2 {
			sendResponse(m, false, t, index(), nil)
		}
		return nil
	}

	sendResponse(m, false, t, index(), nil)
	return pos
}

// writeMedia is writeMediaFile(uniqueId, mediaBuffer, "jpg"), which releases the buffer.
func (d *Decoder) writeMedia(m *ingest.Message, ds *ingest.DeviceSession) string {
	data := d.media.data
	d.media = nil
	return m.WriteMedia(ds.UniqueID, "jpg", data)
}

// decodeSupported decodes the GPS, LBS and status messages; false is Java's return null.
func (d *Decoder) decodeSupported(m *ingest.Message, pos *model.Position, buf *kit.Buf, ds *ingest.DeviceSession,
	t, length int, modelName *string, modelLW, modelSW, modelNT, modelVL bool, modelIs func(string) bool) bool {

	pos.Set(model.KeyType, t)

	if t == msgGpsLbs1 && modelIs("QH302R") {
		buf.Skip(8) // imei
	}

	if (t == msgGpsLbs2 || t == msgGpsLbsDriver) && modelNT {
		buf.ReadUnsignedByte() // location source type
		buf.Skip(8)            // imei
		pos.DeviceTime = model.NewTime(decodeDate(buf, ds))
	}

	if d.hasGps(t) {
		DecodeGps(pos, buf, false, sessionZone(ds))
	} else {
		m.LastLocation(pos, nil)
	}

	if d.hasLbs(t) && buf.ReadableBytes() > 6 {
		hasLength := d.hasStatus(t, modelName) &&
			t != msgLbsStatus &&
			t != msgLbsAlarm &&
			t != msgAlarmModule &&
			(t != msgGpsLbsStatus1 || d.variant != vxt01) &&
			t != msgGpsLbsStatus5
		d.decodeLbs(pos, buf, t, hasLength)
	}

	if d.hasStatus(t, modelName) {
		if t == msgGpsLbsStatus5 {
			buf.ReadUnsignedByte() // network indicator
		}
		d.decodeStatus(pos, buf)
		switch {
		case d.variant == obd6:
			signal := buf.ReadUnsignedShort()
			satellites := kit.BetweenInt(signal, 10, 15) + kit.BetweenInt(signal, 5, 10)
			pos.Set(model.KeySatellites, satellites)
			pos.Set(model.KeyRssi, kit.ToInt(signal, 5))
			pos.AddAlarm(d.decodeAlarm(buf.ReadUnsignedByte(), modelLW, modelSW, modelVL))
			buf.ReadUnsignedByte() // language
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			mode := buf.ReadUnsignedByte()
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
			buf.ReadUnsignedByte()  // reserved
			buf.ReadUnsignedShort() // working time
			if mode == 4 {
				pos.Set(model.PrefixTemp+"1", float64(buf.ReadShort())/10.0)
			}
		case d.variant == seeworld && length == 0x18:
			buf.ReadUnsignedShort() // extension bit status
			buf.ReadUnsignedByte()  // voltage level
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte()))
			buf.ReadUnsignedByte() // language
			buf.ReadUnsignedByte() // sound switch
			buf.ReadUnsignedByte() // light switch
			pos.Set(model.KeySteps, buf.ReadUnsignedInt())
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			pos.Set(model.KeyCharge, buf.ReadUnsignedByte() == 1)
			pos.Set(model.KeyMotion, buf.ReadUnsignedByte() == 1)
		default:
			ntStatus := modelNT && (t == msgGpsLbs2 || t == msgGpsLbsDriver)
			if t == msgGpsLbsStatus5 || ntStatus {
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
			}
			if t == msgStatus && modelIs("R11") {
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
			} else {
				battery := buf.ReadUnsignedByte()
				switch {
				case ntStatus:
					pos.Set(model.KeyBattery, float64(battery)/10.0)
				case battery <= 6:
					pos.Set(model.KeyBatteryLevel, battery*100/6)
				case battery <= 100:
					pos.Set(model.KeyBatteryLevel, battery)
				}
			}
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			if t == msgStatus && modelLW {
				pos.Set(model.KeyPower, float64(kit.ToInt(buf.ReadUnsignedShort(), 12))/10.0)
			} else {
				extension := buf.ReadUnsignedByte()
				if t == msgGpsLbsStatus3 || t == msgFenceMulti || t == msgAlarmModule {
					extension += (buf.GetUnsignedByte(buf.ReaderIndex()) >> 4) << 8
				}
				if t == msgStatus && modelSW {
					pos.Set(model.KeyPower, float64(extension))
				} else if d.variant != vxt01 {
					pos.AddAlarm(d.decodeAlarm(extension, modelLW, modelSW, modelVL))
					if d.variant == trx16i {
						pos.Set(model.KeyEvent, extension)
					}
				}
			}
		}
	}

	if t == msgStatus2 || t == msgGpsLbsStatus5 || t == msgGpsLbsStatus3 ||
		t == msgFenceMulti || t == msgAlarmModule ||
		modelNT && (t == msgGpsLbs2 || t == msgGpsLbsDriver) {
		buf.ReadUnsignedByte() // language
	}

	if t == msgGpsLbsStatus1 && d.variant == trx16i {
		buf.ReadUnsignedByte() // language
		if id := buf.ReadSlice(8).HexDump(); !allZeros(id) {
			pos.Set(model.KeyDriverUniqueID, id)
		}
	}

	if t == msgStatus2 || t == msgAlarmModule {
		for buf.ReadableBytes() > 6 {
			moduleType := buf.ReadUnsignedShort()
			moduleLength := buf.ReadUnsignedByte()
			switch moduleType {
			case 0x0018:
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
			case 0x0032:
				pos.Set("startupStatus", buf.ReadUnsignedByte())
			case 0x006A:
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			default:
				buf.Skip(moduleLength)
			}
		}
	}

	if t == msgGpsLbsStatus5 {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		pos.Set(model.KeyHours, buf.ReadUnsignedInt()*1000)
		buf.Skip(8) // terminal id
		pos.Set(model.KeyInput, buf.ReadUnsignedShort())
	}

	if t == msgGpsLbs1 {
		d.decodeGpsLbs1(pos, buf, modelLW, modelSW, modelVL)
	}

	if t == msgGpsLbs2 && d.variant == seeworld {
		pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
		buf.ReadUnsignedByte() // reporting mode
		buf.ReadUnsignedByte() // supplementary transmission
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		buf.ReadUnsignedInt() // travel time
		temperature := buf.ReadUnsignedShort()
		if kit.Check(int64(temperature), 15) {
			temperature = -kit.ToInt(temperature, 15)
		}
		pos.Set(model.PrefixTemp+"1", float64(temperature)/100.0)
		pos.Set(model.KeyHumidity, float64(buf.ReadUnsignedShort())/100.0)
	}

	if t == msgGpsLbsStatus4 && d.variant == sl4x {
		pos.Altitude = float64(buf.ReadShort())
	}

	if (t == msgGpsLbs2 || t == msgGpsLbs3 || t == msgGpsLbs4 || t == msgGpsLbs5) &&
		buf.ReadableBytes() >= 3+6 && !modelNT {
		pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
		pos.Set(model.KeyEvent, buf.ReadUnsignedByte()) // reason
		pos.Set(model.KeyArchive, buf.ReadUnsignedByte() > 0)
		if d.variant == sl4x {
			if buf.ReadableBytes() > 2+6 {
				pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
			}
			pos.Altitude = float64(buf.ReadShort())
		}
	}

	if t == msgGpsLbs3 {
		module := buf.ReadUnsignedShort()
		subLength := buf.ReadUnsignedByte()
		switch module {
		case 0x0027:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		case 0x002E:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		case 0x003B:
			pos.Accuracy = float64(buf.ReadUnsignedShort()) / 100.0
		default:
			buf.Skip(subLength)
		}
	}

	if t == msgGpsLbsRfid {
		pos.Set(model.KeyDriverUniqueID, buf.ReadSlice(8).HexDump())
		buf.ReadUnsignedByte() // validity
	}

	if modelNT && (t == msgGpsLbs2 || t == msgGpsLbsDriver) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedMedium())
		pos.Set(model.KeyHours, int64(buf.ReadUnsignedMedium())*60*1000)
	}

	if modelNT && t == msgGpsLbsDriver {
		driverLength := buf.ReadUnsignedByte()
		if driverLength > 0 {
			buf.ReadUnsignedByte() // driver identifier type
			pos.Set(model.KeyDriverUniqueID, buf.ReadSlice(driverLength-1).HexDump())
		}
	}

	if buf.ReadableBytes() == 3+6 || buf.ReadableBytes() == 3+4+6 {
		pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
		buf.ReadUnsignedByte() // upload mode
		if buf.ReadUnsignedByte() > 0 {
			pos.Set(model.KeyArchive, true)
		}
	}

	if buf.ReadableBytes() == 4+6 {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
	}

	if t == msgGpsLbsStatus3 || t == msgFenceMulti {
		pos.Set(model.KeyGeofence, buf.ReadUnsignedByte())
	}
	return true
}

func (d *Decoder) decodeGpsLbs1(pos *model.Position, buf *kit.Buf, modelLW, modelSW, modelVL bool) {
	switch d.variant {
	case gt06eCard:
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		data := buf.ReadString(buf.ReadUnsignedByte())
		buf.ReadUnsignedByte() // alarm
		buf.ReadUnsignedByte() // swiped
		pos.Set(model.KeyCard, kit.Trim(data))
	case benway:
		mask := buf.ReadUnsignedShort()
		mk := int64(mask)
		pos.Set(model.KeyIgnition, kit.Check(mk, 8+7))
		pos.Set(model.PrefixIn+"2", kit.Check(mk, 8+6))
		if kit.Check(mk, 8+4) {
			value := kit.ToInt(mask, 8+1)
			if kit.Check(mk, 8+1) {
				value = -value
			}
			pos.Set(model.PrefixTemp+"1", value)
		} else {
			value := kit.ToInt(mask, 8+2)
			if kit.Check(mk, 8+5) {
				pos.Set(model.PrefixAdc+"1", value)
			} else {
				pos.Set(model.PrefixAdc+"1", float64(value)/10.0)
			}
		}
	case vxt01:
		d.decodeStatus(pos, buf)
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		buf.ReadUnsignedByte() // alarm extension
	case s5:
		d.decodeStatus(pos, buf)
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		pos.AddAlarm(d.decodeAlarm(buf.ReadUnsignedByte(), modelLW, modelSW, modelVL))
		pos.Set("oil", buf.ReadUnsignedShort())
		temperature := buf.ReadUnsignedByte()
		if kit.Check(int64(temperature), 7) {
			temperature = -kit.ToInt(temperature, 7)
		}
		pos.Set(model.PrefixTemp+"1", temperature)
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*10)
	case wetrust:
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		pos.Set(model.KeyCard, buf.ReadString(buf.ReadUnsignedByte()))
		if buf.ReadUnsignedByte() > 0 {
			pos.AddAlarm(model.AlarmGeneral)
		}
		pos.Set("cardStatus", buf.ReadUnsignedByte())
		pos.Set(model.KeyDrivingTime, buf.ReadUnsignedShort())
	}
}

func (d *Decoder) decodeInfo(m *ingest.Message, pos *model.Position, buf *kit.Buf, ds *ingest.DeviceSession, length int) any {
	subType := buf.ReadUnsignedByte()

	m.LastLocation(pos, nil)

	switch subType {
	case 0x00:
		pos.Set(model.PrefixAdc+"1", float64(buf.ReadUnsignedShort())/100.0)
		return pos
	case 0x04:
		content := buf.ReadString(buf.ReadableBytes() - 4 - 2)
		for _, value := range kit.Split(content, ";") {
			pair := kit.Split(value, "=")
			switch pair[0] {
			case "ALM1", "ALM2", "ALM3", "ALM4":
				pos.Set("alarm"+string(kit.CharAt(pair[0], 3))+"Status", kit.IntRadix(pair[1], 16))
			case "STA1":
				pos.Set("otherStatus", kit.IntRadix(pair[1], 16))
			case "DYD":
				pos.Set("engineStatus", kit.IntRadix(pair[1], 16))
			}
		}
		return pos
	case 0x05:
		if buf.ReadableBytes() >= 6+1+6 {
			pos.DeviceTime = model.NewTime(decodeDate(buf, ds))
		}
		flags := int64(buf.ReadUnsignedByte())
		pos.Set(model.KeyDoor, kit.Check(flags, 0))
		pos.Set(model.PrefixIo+"1", kit.Check(flags, 2))
		return pos
	case 0x0a:
		buf.Skip(8) // imei
		buf.Skip(8) // imsi
		pos.Set(model.KeyIccid, kit.StripTrailing('f', buf.ReadSlice(10).HexDump()))
		return pos
	case 0x0b:
		switch length - 6 {
		case 1:
			pos.Set("networkTechnology", pick(buf.ReadSignedByte() > 0, "4G", "2G"))
		case 2:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		}
		return pos
	case 0x0d:
		if buf.GetByte(buf.ReaderIndex()) != '!' {
			buf.Skip(6)
		}
		parser := kit.NewParser(patternFuel, kit.Latin1(buf.GetBytes(buf.ReaderIndex(), buf.ReadableBytes()-4-2)))
		if !parser.Matches() {
			return nil
		}
		pos.Set(model.PrefixTemp+"1", parser.NextDoubleDefault(0))
		pos.Set(model.KeyFuel, parser.NextDoubleDefault(0))
		return pos
	case 0x1b:
		if unicode.IsLetter(rune(buf.GetUnsignedByte(buf.ReaderIndex()))) {
			pos.Set("serial", kit.Trim(buf.ReadString(buf.ReadableBytes()-6)))
		} else {
			buf.ReadUnsignedByte() // header
			buf.ReadUnsignedByte() // type
			pos.Set(model.KeyDriverUniqueID, buf.ReadSlice(4).HexDump())
			buf.ReadUnsignedByte() // checksum
			buf.ReadUnsignedByte() // footer
		}
		return pos
	case 0x1e:
		pos.Set(model.PrefixTemp+"1", float64(buf.ReadInt())/10.0)
		pos.Set(model.KeyHumidity, float64(buf.ReadInt())/10.0)
		return pos
	}
	return nil
}

func (d *Decoder) decodeModular(m *ingest.Message, pos *model.Position, buf *kit.Buf) {
	for buf.ReadableBytes() > 6 {
		moduleType := buf.ReadUnsignedShort()
		moduleLength := buf.ReadUnsignedShort()
		switch moduleType {
		case 0x03:
			pos.Set(model.KeyIccid, buf.ReadSlice(10).HexDump())
		case 0x09:
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		case 0x0a:
			pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedByte())
		case 0x11:
			cellTower := model.NewCellTowerRSSI(
				buf.ReadUnsignedShort(),
				buf.ReadUnsignedShort(),
				buf.ReadUnsignedShort(),
				int64(buf.ReadUnsignedMedium()),
				buf.ReadUnsignedByte())
			if *cellTower.CellID > 0 {
				pos.Network = model.NetworkOf(cellTower)
			}
		case 0x18:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		case 0x28:
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
		case 0x29:
			pos.Set(model.KeyIndex, buf.ReadUnsignedInt())
		case 0x2a:
			input := buf.ReadUnsignedByte()
			pos.Set(model.KeyDoor, kit.ToInt(input, 4) > 0)
			pos.Set("tamper", kit.FromInt(input, 4) > 0)
		case 0x2b:
			pos.Set("bootReason", buf.ReadUnsignedByte())
		case 0x2e:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
		case 0x33:
			pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			pos.Altitude = float64(buf.ReadShort())

			latitude := float64(buf.ReadUnsignedInt()) / 60.0 / 30000.0
			longitude := float64(buf.ReadUnsignedInt()) / 60.0 / 30000.0
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

			flags := buf.ReadUnsignedShort()
			f := int64(flags)
			pos.Course = float64(kit.ToInt(flags, 10))
			pos.Valid = kit.Check(f, 12)
			if !kit.Check(f, 10) {
				latitude = -latitude
			}
			if kit.Check(f, 11) {
				longitude = -longitude
			}
			pos.SetLatitude(latitude)
			pos.SetLongitude(longitude)
		case 0x34:
			event := buf.ReadUnsignedByte()
			switch event {
			case 0x10:
				pos.AddAlarm(model.AlarmDoor)
			case 0x12:
				pos.AddAlarm(model.AlarmRemoving)
			case 0x14:
				pos.AddAlarm(model.AlarmGeofenceEnter)
			case 0x15:
				pos.AddAlarm(model.AlarmGeofenceExit)
			case 0x16:
				pos.AddAlarm(model.AlarmTemperature)
			case 0x1A:
				pos.AddAlarm(model.AlarmLowBattery)
			case 0x1B:
				pos.AddAlarm(model.AlarmOverspeed)
			case 0x1C:
				pos.AddAlarm(model.AlarmPowerOn)
			case 0x1D:
				pos.AddAlarm(model.AlarmPowerOff)
			case 0x20:
				pos.AddAlarm(model.AlarmVibration)
			case 0x21:
				pos.AddAlarm(model.AlarmPowerCut)
			case 0x23:
				pos.AddAlarm(model.AlarmTampering)
			}
			pos.Set(model.KeyEvent, event)
			buf.ReadUnsignedIntLE()          // time
			buf.Skip(buf.ReadUnsignedByte()) // content
		case 0x7f:
			pos.Set("tag1Id", buf.ReadSlice(6).HexDump())
			pos.Set("tag1Battery", float64(buf.ReadUnsignedShort())/1000.0)
			pos.Set("tag1BatteryLevel", buf.ReadUnsignedByte())
			pos.Set("tag1Temp", float64(buf.ReadShort())/10.0)
			pos.Set("tag1Humidity", buf.ReadShort())
			pos.Set("tag1Status", buf.ReadUnsignedByte())
		default:
			buf.Skip(moduleLength)
		}
	}
}

func (d *Decoder) decodePeripheral(pos *model.Position, buf *kit.Buf, t int) {
	for buf.ReadableBytes() > 6 {
		statusID := buf.ReadUnsignedShort()
		if t == msgPeripheral {
			switch statusID {
			case 0x000A:
				statusLength := buf.ReadUnsignedShort()
				buf.ReadUnsignedByte() // mac address type
				pos.Set("mac", buf.ReadSlice(6).HexDump())
				event := buf.ReadUnsignedByte()
				pos.Set(model.KeyEvent, event)
				eventData := buf.ReadUnsignedByte()
				if event > 0 {
					pos.Set("eventData", eventData)
				}
				pos.Set("dataType", buf.ReadUnsignedByte())
				pos.Set("dataDetails", buf.ReadSlice(statusLength-10).HexDump())
			case 0x000C:
				buf.ReadUnsignedByte() // length
				pos.Set("externalBatteryLevel", buf.ReadUnsignedByte())
				if buf.ReadUnsignedByte() > 0 {
					pos.Set("externalBatteryCharge", true)
				}
				pos.Set("externalBatteryCycles", buf.ReadUnsignedShort())
				buf.Skip(6)
			default:
				statusLength := buf.ReadUnsignedByte()
				if statusLength == 0 {
					buf.Skip(buf.ReadUnsignedByte())
				} else {
					buf.Skip(statusLength)
				}
			}
		} else {
			statusLength := buf.ReadUnsignedByte()
			switch statusID {
			case 0x0001:
				motionStatus := buf.ReadUnsignedByte()
				pos.Set(model.KeyMotion, motionStatus == 0x02 || motionStatus == 0x04)
			case 0x0002:
				pos.Set(model.KeyArmed, buf.ReadUnsignedByte() > 0)
			case 0x0004:
				pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
			case 0x0005:
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
			case 0x0007:
				pos.Set(model.KeyCharge, buf.ReadUnsignedByte() > 0)
			case 0x000A:
				pos.Set("chargeVoltage", buf.ReadUnsignedShort())
			case 0x000B:
				pos.Set("batteryTemp", float64(buf.ReadUnsignedShort())/10.0)
			default:
				buf.Skip(statusLength)
			}
		}
	}
}

// encodeContent is Gt06ProtocolEncoder.encodeContent.
func encodeContent(m *ingest.Message, deviceID int64, content string) *kit.Buf {
	language := false
	if v := m.LookupBool(deviceID, m.Protocol()+".language", false, true); v != nil {
		language = *v
	}

	contentLength := kit.Len(content)
	buf := kit.Empty()
	buf.WriteByte8(0x78)
	buf.WriteByte8(0x78)
	length := 1 + 1 + 4 + contentLength + 2 + 2 // message length
	if language {
		length += 2
	}
	buf.WriteByte8(length)
	buf.WriteByte8(msgCommand0)
	buf.WriteByte8(4 + contentLength) // command length
	buf.WriteInt(0)
	buf.WriteBytes(kit.ASCIIBytes(content)) // command
	if language {
		buf.WriteShort(2) // english language
	}
	buf.WriteShort(0) // message index
	buf.WriteShort(kit.CRC16(kit.CRC16_X25, buf.Array()[2:buf.WriterIndex()]))
	buf.WriteByte8('\r')
	buf.WriteByte8('\n')
	return buf
}

// encode is Gt06ProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	alternative := false
	if v := m.LookupBool(c.DeviceID, m.Protocol()+".alternative", false, true); v != nil {
		alternative = *v
	}
	password := m.DevicePassword(c.DeviceID, m.Protocol(), defaultDevicePasswd)
	device, err := m.Device(c.DeviceID)
	if err != nil {
		panic(err)
	}
	if device == nil {
		kit.Throw("NullPointerException: device %d", c.DeviceID)
	}
	isG109 := device.Model != nil && *device.Model == "G109"

	switch c.Type {
	case model.CommandEngineStop:
		switch {
		case isG109:
			return encodeContent(m, c.DeviceID, "DYD#")
		case alternative:
			return encodeContent(m, c.DeviceID, "DYD,"+password+"#")
		}
		return encodeContent(m, c.DeviceID, "Relay,1#")
	case model.CommandEngineResume:
		switch {
		case isG109:
			return encodeContent(m, c.DeviceID, "HFYD#")
		case alternative:
			return encodeContent(m, c.DeviceID, "HFYD,"+password+"#")
		}
		return encodeContent(m, c.DeviceID, "Relay,0#")
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: data")
		}
		return encodeContent(m, c.DeviceID, *data)
	}
	return nil
}
