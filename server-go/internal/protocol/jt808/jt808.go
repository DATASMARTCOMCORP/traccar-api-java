// Package jt808 is the JT/T 808 protocol (Jt808Protocol): escaped binary frames between
// 0x7e (or 0xe7) delimiters, plus '(...)' text results, over TCP.
package jt808

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/jt600"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "jt808",
		DataCommands: protocol.DataCommands("jt808"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			return ingest.Pipeline{
				Framer:       Framer(),
				Decoder:      d,
				Encoder:      &Encoder{Decoder: d},
				FrameEncoder: FrameEncode,
			}
		}}},
	}
}

// Framer is Jt808FrameDecoder: '(...)' text, or frames between 0x7e delimiters
// (0x7d escapes) or 0xe7 delimiters (0xe6 and 0x3e escapes).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		for buf.IsReadable() {
			b := buf.GetUnsignedByte(buf.ReaderIndex())
			if b == '(' || b == 0x7e || b == 0xe7 {
				break
			}
			buf.Skip(1)
		}
		if buf.ReadableBytes() < 2 {
			return nil
		}
		first := buf.GetUnsignedByte(buf.ReaderIndex())
		if first == '(' {
			index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), ')')
			if index >= 0 {
				// readRetainedSlice(index + 1): Java's length is the absolute index plus one
				return buf.ReadSlice(index + 1)
			}
			return nil
		}
		alternative := first == 0xe7
		index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), byte(first))
		if index < 0 {
			return nil
		}
		result := kit.Empty()
		for buf.ReaderIndex() <= index {
			b := buf.ReadUnsignedByte()
			switch {
			case alternative && (b == 0xe6 || b == 0x3e):
				ext := buf.ReadUnsignedByte()
				switch {
				case b == 0xe6 && ext == 0x01:
					result.WriteByte8(0xe6)
				case b == 0xe6 && ext == 0x02:
					result.WriteByte8(0xe7)
				case b == 0x3e && ext == 0x01:
					result.WriteByte8(0x3e)
				case b == 0x3e && ext == 0x02:
					result.WriteByte8(0x3d)
				}
			case !alternative && b == 0x7d:
				switch buf.ReadUnsignedByte() {
				case 0x01:
					result.WriteByte8(0x7d)
				case 0x02:
					result.WriteByte8(0x7e)
				}
			default:
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

// FrameEncode is Jt808FrameEncoder: it escapes a message's bytes.
func FrameEncode(msg []byte) []byte {
	if len(msg) == 0 {
		return msg
	}
	alternative := msg[0] == 0xe7
	out := make([]byte, 0, len(msg)+8)
	for i, b := range msg {
		readable := i+1 < len(msg)
		switch {
		case alternative && (b == 0xe6 || b == 0x3d || b == 0x3e):
			if b == 0xe6 {
				out = append(out, 0xe6)
			} else {
				out = append(out, 0x3e)
			}
			if b == 0x3d {
				out = append(out, 0x02)
			} else {
				out = append(out, 0x01)
			}
		case alternative && b == 0xe7 && i != 0 && readable:
			out = append(out, 0xe6, 0x02)
		case !alternative && b == 0x7d:
			out = append(out, 0x7d, 0x01)
		case !alternative && b == 0x7e && i != 0 && readable:
			out = append(out, 0x7d, 0x02)
		default:
			out = append(out, b)
		}
	}
	return out
}

// Message types.
const (
	msgTerminalGeneralResponse  = 0x0001
	msgGeneralResponse          = 0x8001
	msgGeneralResponse2         = 0x4401
	msgHeartbeat                = 0x0002
	msgTerminalLogout           = 0x0003
	msgHeartbeat2               = 0x0506
	msgTerminalRegister         = 0x0100
	msgTerminalRegisterResponse = 0x8100
	msgTerminalControl          = 0x8105
	msgTerminalAuth             = 0x0102
	msgTerminalAttributes       = 0x0107
	msgLocationReport           = 0x0200
	msgLocationBatch2           = 0x0210
	msgAcceleration             = 0x2070
	msgLocationReport2          = 0x5501
	msgLocationReportBlind      = 0x5502
	msgLocationBatch            = 0x0704
	msgOilControl               = 0xA006
	msgTimeSyncRequest          = 0x0109
	msgTimeSyncResponse         = 0x8109
	msgTimezoneSync             = 0x1007
	msgPhoto                    = 0x8888
	msgTransparent              = 0x0900
	msgTransparentDownlink      = 0x8900
	msgParameterSetting         = 0x0310
	msgSendTextMessage          = 0x8300
	msgReportTextMessage        = 0x6006
	msgConfigurationParameters  = 0x8103
	msgCommandResponse          = 0x0701
	msgTextMessageResponse      = 0x1300
	msgDriverIdentity           = 0x0702
	msgVideoRequest             = 0x9101
	msgVideoControl             = 0x9102

	resultSuccess = 0
)

var dateFormat = kit.NewDateFormat("yyyyMMddHHmmss", time.UTC)

// Decoder is Jt808ProtocolDecoder. The delimiter and protocol version of the last
// message are kept for replies and commands, as in Java.
type Decoder struct {
	// ModelOverride is setModelOverride: the device model to assume ("" for none).
	ModelOverride string

	delimiter       int
	protocolVersion *int
}

func (d *Decoder) delim() int {
	if d.delimiter == 0 {
		return 0x7e
	}
	return d.delimiter
}

// ProtocolVersion is getProtocolVersion (nil for none).
func (d *Decoder) ProtocolVersion() *int { return d.protocolVersion }

// FormatMessage is formatMessage(type, id, shortIndex, data).
func (d *Decoder) FormatMessage(typ int, id []byte, shortIndex bool, data *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(d.delim())
	buf.WriteShort(typ)
	attribute := data.ReadableBytes()
	if d.protocolVersion != nil {
		attribute |= 0x4000
	}
	buf.WriteShort(attribute)
	if d.protocolVersion != nil {
		buf.WriteByte8(*d.protocolVersion)
	}
	buf.WriteBytes(id)
	if shortIndex {
		buf.WriteByte8(1)
	} else {
		buf.WriteShort(0)
	}
	buf.WriteBuf(data)
	b := buf.Bytes()
	buf.WriteByte8(kit.XOR(b[1:]))
	buf.WriteByte8(d.delim())
	return buf
}

func (d *Decoder) model(m *ingest.Message, ds *ingest.DeviceSession) string {
	if d.ModelOverride != "" {
		return d.ModelOverride
	}
	return m.DeviceModel(ds)
}

func (d *Decoder) sendGeneralResponse(m *ingest.Message, id []byte, typ, index int) {
	if m.HasChannel() {
		response := kit.Empty()
		response.WriteShort(index)
		response.WriteShort(typ)
		response.WriteByte8(resultSuccess)
		m.Send(d.FormatMessage(msgGeneralResponse, id, false, response))
	}
}

func (d *Decoder) sendGeneralResponse2(m *ingest.Message, id []byte, typ int) {
	if m.HasChannel() {
		response := kit.Empty()
		response.WriteShort(typ)
		response.WriteByte8(resultSuccess)
		m.Send(d.FormatMessage(msgGeneralResponse2, id, true, response))
	}
}

func (d *Decoder) requestAttachments(m *ingest.Message, id []byte, pos *model.Position) {
	if !m.HasChannel() || pos == nil || !pos.Attributes.Has("alarmLabel") {
		return
	}
	webURL, ok := m.Config().Lookup("web.url")
	uploadPort, err := m.Config().Int("jimiphoto.port", 0)
	if err != nil {
		panic(err)
	}
	if !ok || uploadPort <= 0 {
		return
	}
	text := kit.Format("VIDEOUPLOAD,%s,%d,%s,1-2-3,2", uriHost(webURL), uploadPort, *pos.Attributes.JavaString("alarmLabel"))
	data := kit.Empty()
	data.WriteByte8(0xF0)
	data.WriteASCII(text)
	m.Send(d.FormatMessage(msgTransparentDownlink, id, false, data))
}

// uriHost is URI.create(s).getHost() (nil, here "null", without a host).
func uriHost(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		kit.Throw("IllegalArgumentException: %v", err)
	}
	if u.Hostname() == "" {
		return "null"
	}
	return u.Hostname()
}

var (
	alarmModelsTamper   = map[string]bool{"G-360P": true, "G-508P": true}
	alarmModelsMovement = map[string]bool{"AL300": true, "GL100": true}
	jcModels            = map[string]bool{"JC371": true, "JC181": true, "JC182": true, "JC450": true, "JC451": true}
)

func alarmIf(pos *model.Position, cond bool, alarm string) {
	if cond {
		pos.AddAlarm(alarm)
	}
}

func decodeAlarm(pos *model.Position, mdl string, value int64) {
	c := func(i int) bool { return kit.Check(value, i) }
	switch {
	case alarmModelsTamper[mdl]:
		alarmIf(pos, c(0) || c(4), model.AlarmRemoving)
		alarmIf(pos, c(1), model.AlarmTampering)
	case alarmModelsMovement[mdl]:
		alarmIf(pos, c(16), model.AlarmMovement)
	default:
		alarmIf(pos, c(0), model.AlarmSos)
		alarmIf(pos, c(1), model.AlarmOverspeed)
		alarmIf(pos, c(2), model.AlarmFatigueDriving)
		alarmIf(pos, c(5) || c(6), model.AlarmGpsAntennaCut)
		alarmIf(pos, c(4) || c(9) || c(10) || c(11), model.AlarmFault)
		alarmIf(pos, c(7) || c(18), model.AlarmLowBattery)
		alarmIf(pos, c(8), model.AlarmPowerOff)
		alarmIf(pos, c(15), model.AlarmVibration)
		alarmIf(pos, c(16) || c(17), model.AlarmTampering)
		alarmIf(pos, c(19), model.AlarmParking)
		alarmIf(pos, c(20), model.AlarmGeofence)
		alarmIf(pos, c(26), model.AlarmGeneral)
		alarmIf(pos, c(28), model.AlarmMovement)
		alarmIf(pos, (c(29) || c(30)) && mdl != "VL300", model.AlarmAccident)
	}
}

func readSignedWord(buf *kit.Buf) int {
	value := buf.ReadUnsignedShort()
	if kit.Check(int64(value), 15) {
		return -kit.ToInt(value, 15)
	}
	return kit.ToInt(value, 15)
}

func readDate(buf *kit.Buf, zone *time.Location) time.Time {
	return kit.NewDateBuilder(zone).
		SetYear(kit.ReadBCD(buf, 2)).
		SetMonth(kit.ReadBCD(buf, 2)).
		SetDay(kit.ReadBCD(buf, 2)).
		SetHour(kit.ReadBCD(buf, 2)).
		SetMinute(kit.ReadBCD(buf, 2)).
		SetSecond(kit.ReadBCD(buf, 2)).
		Date()
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// DecodeID is decodeId: the BCD id, or an IMEI (with its Luhn digit) when it isn't BCD.
func DecodeID(id *kit.Buf) string {
	serial := id.HexDump()
	if digits.MatchString(serial) {
		return serial
	}
	imei := int64(id.GetUnsignedShort(0))
	imei = imei<<32 + id.GetUnsignedInt(2)
	return strconv.FormatInt(imei, 10) + strconv.FormatInt(kit.Luhn(imei), 10)
}

// EncodeID is encodeId(uniqueId, length).
func EncodeID(uniqueID string, length int) []byte {
	if length == 10 {
		return kit.ParseHex(strings.ReplaceAll(kit.Format("%20s", uniqueID), " ", "0"))
	}
	if kit.Len(uniqueID)%2 == 0 {
		return kit.ParseHex(uniqueID)
	}
	imei := kit.Long(kit.Substring(uniqueID, 0, kit.Len(uniqueID)-1))
	buf := kit.Empty()
	buf.WriteShort(int(int32(imei >> 32)))
	buf.WriteInt(int64(int32(imei)))
	return buf.Bytes()
}

func decodeObdRt(pos *model.Position, data string) {
	values := kit.Split(data, ",")
	index := 1 // skip header
	next := func() (string, bool) {
		v := values[index]
		index++
		return v, v != ""
	}
	setDouble := func(key string) {
		if v, ok := next(); ok {
			pos.Set(key, kit.Double(v))
		}
	}
	setDouble(model.KeyPower)
	setDouble(model.KeyRpm)
	setDouble(model.KeyObdSpeed)
	setDouble(model.KeyThrottle)
	setDouble(model.KeyEngineLoad)
	if v, ok := next(); ok {
		pos.Set(model.KeyCoolantTemp, kit.Int(v))
	}
	setDouble(model.KeyFuelConsumption) // instant
	setDouble(model.KeyFuelConsumption) // average
	setDouble(model.KeyOdometerTrip)
	setDouble(model.KeyObdOdometer)
	setDouble("tripFuelUsed")
	setDouble(model.KeyFuelUsed)
}

// gbk is Java's GBK charset (decoding replaces malformed input with U+FFFD).
func gbkDecode(b []byte) string {
	s, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil {
		return kit.ASCII(b)
	}
	return string(s)
}

// gbkEncode is String.getBytes(GBK): unmappable characters become '?'.
func gbkEncode(s string) []byte {
	var out []byte
	enc := simplifiedchinese.GBK.NewEncoder()
	for _, r := range s {
		if r < 0x80 {
			out = append(out, byte(r))
			continue
		}
		b, err := enc.Bytes([]byte(string(r)))
		if err != nil {
			out = append(out, '?')
			continue
		}
		out = append(out, b...)
	}
	return out
}

// utf16BE is new String(bytes, UTF_16BE): malformed input becomes U+FFFD.
func utf16BE(b []byte) string {
	units := make([]uint16, 0, len(b)/2+1)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	s := string(utf16.Decode(units))
	if len(b)%2 != 0 {
		s += "�"
	}
	return s
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.GetByte(buf.ReaderIndex()) == '(' {
		sentence := kit.Latin1(buf.Bytes())
		if strings.Contains(sentence, "BASE,2") {
			response := strings.ReplaceAll(sentence, "TIME", m.Now.UTC().Format("20060102150405"))
			m.Send(kit.NewBuf(kit.NettyBytes(response)))
			return nil
		}
		return decodeResult(m, sentence)
	}

	d.delimiter = buf.ReadUnsignedByte()
	typ := buf.ReadUnsignedShort()
	attribute := buf.ReadUnsignedShort()

	bodyLength := kit.ToInt(attribute, 10)

	if kit.Check(int64(attribute), 14) {
		v := buf.ReadUnsignedByte()
		d.protocolVersion = &v
	} else {
		d.protocolVersion = nil
	}
	idLength := 6
	if d.protocolVersion != nil {
		idLength = 10
	} else if d.delimiter == 0xe7 {
		idLength = 7
	}
	idBuf := buf.ReadSlice(idLength)
	id := idBuf.Bytes()

	var index int
	if typ == msgLocationReport2 || typ == msgLocationReportBlind {
		index = buf.ReadUnsignedByte()
	} else {
		index = buf.ReadUnsignedShort()
	}

	uniqueID := DecodeID(idBuf)
	strippedID := kit.StripLeading('0', uniqueID)
	var ds *ingest.DeviceSession
	if uniqueID == strippedID {
		ds = m.Session(uniqueID)
	} else {
		ds = m.Session(strippedID, uniqueID)
	}
	if ds == nil {
		return nil
	}

	zone, _ := ds.Get("timezone").(*time.Location)
	if zone == nil {
		z, err := m.TimeZone(ds.DeviceID, "GMT+8")
		if err != nil {
			panic(err)
		}
		zone = z
		ds.Set("timezone", zone)
	}

	newPosition := func() *model.Position {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		return pos
	}

	switch {
	case typ == msgTerminalRegister:
		if m.HasChannel() {
			response := kit.Empty()
			response.WriteShort(index)
			response.WriteByte8(resultSuccess)
			response.WriteBytes(kit.ASCIIBytes(DecodeID(idBuf)))
			m.Send(d.FormatMessage(msgTerminalRegisterResponse, id, false, response))
		}

	case typ == msgReportTextMessage:
		d.sendGeneralResponse(m, id, typ, index)
		pos := newPosition()
		m.LastLocation(pos, nil)
		buf.ReadUnsignedByte() // encoding
		pos.Set(model.KeyResult, gbkDecode(buf.ReadBytes(buf.ReadableBytes()-2)))
		return pos

	case typ == msgTextMessageResponse:
		pos := newPosition()
		m.LastLocation(pos, nil)
		buf.ReadUnsignedShort() // response serial number
		pos.Set(model.KeyResult, kit.Trim(utf16BE(buf.ReadBytes(bodyLength-2))))
		return pos

	case typ == msgHeartbeat:
		d.sendGeneralResponse(m, id, typ, index)
		if buf.ReadableBytes() >= 3+1 {
			pos := newPosition()
			m.LastLocation(pos, nil)
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
			return pos
		}

	case typ == msgTerminalAuth || typ == msgHeartbeat2 || typ == msgPhoto ||
		typ == msgTerminalLogout || typ == msgTimezoneSync:
		d.sendGeneralResponse(m, id, typ, index)

	case typ == msgTerminalAttributes:
		d.sendGeneralResponse(m, id, typ, index)
		pos := newPosition()
		m.LastLocation(pos, nil)
		buf.ReadUnsignedShort() // terminal type
		buf.Skip(5)             // manufacturer id
		buf.Skip(20)            // terminal model
		buf.Skip(7)             // terminal id
		pos.Set(model.KeyIccid, buf.ReadSlice(10).HexDump())
		return pos

	case typ == msgLocationReport:
		d.sendGeneralResponse(m, id, typ, index)
		pos := d.decodeLocation(m, ds, zone, buf)
		d.requestAttachments(m, id, pos)
		return pos

	case typ == msgLocationReport2 || typ == msgLocationReportBlind:
		if kit.Check(int64(attribute), 15) {
			d.sendGeneralResponse2(m, id, typ)
		}
		return decodeLocation2(m, ds, buf, typ)

	case typ == msgLocationBatch2 && bodyLength == 7:
		d.sendGeneralResponse(m, id, typ, index)
		pos := newPosition()
		pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		t := readDate(buf, zone)
		m.LastLocation(pos, &t)
		return pos

	case typ == msgLocationBatch || typ == msgLocationBatch2:
		d.sendGeneralResponse(m, id, typ, index)
		return d.decodeLocationBatch(m, ds, zone, buf, typ)

	case typ == msgTimeSyncRequest:
		if m.HasChannel() {
			now := m.Now.UTC()
			response := kit.Empty()
			response.WriteShort(now.Year())
			response.WriteByte8(int(now.Month()))
			response.WriteByte8(now.Day())
			response.WriteByte8(now.Hour())
			response.WriteByte8(now.Minute())
			response.WriteByte8(now.Second())
			response.WriteByte8(resultSuccess)
			m.Send(d.FormatMessage(msgTimeSyncResponse, id, false, response))
		}

	case typ == msgAcceleration:
		pos := newPosition()
		m.LastLocation(pos, nil)
		var data strings.Builder
		data.WriteString("[")
		for buf.ReadableBytes() > 2 {
			buf.Skip(6) // time
			if data.Len() > 1 {
				data.WriteString(",")
			}
			data.WriteString("[")
			data.WriteString(strconv.Itoa(readSignedWord(buf)))
			data.WriteString(",")
			data.WriteString(strconv.Itoa(readSignedWord(buf)))
			data.WriteString(",")
			data.WriteString(strconv.Itoa(readSignedWord(buf)))
			data.WriteString("]")
		}
		data.WriteString("]")
		pos.Set(model.KeyGSensor, data.String())
		return pos

	case typ == msgTransparent:
		d.sendGeneralResponse(m, id, typ, index)
		return d.decodeTransparent(m, ds, zone, buf)

	case typ == msgCommandResponse:
		pos := newPosition()
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, buf.ReadString(int(buf.ReadInt())))
		return pos

	case typ == msgDriverIdentity:
		pos := newPosition()
		m.LastLocation(pos, nil)
		pos.Set("cardStatus", buf.ReadUnsignedByte())
		pos.DeviceTime = model.NewTime(readDate(buf, zone))
		pos.Set("cardResult", buf.ReadUnsignedByte())
		pos.Set("driver", buf.ReadString(buf.ReadUnsignedByte()))
		pos.Set("cardCode", kit.Trim(buf.ReadString(20)))
		pos.Set("cardAgency", buf.ReadString(buf.ReadUnsignedByte()))
		pos.Set("cardValidity", buf.ReadSlice(4).HexDump())
		return pos
	}

	return nil
}

func decodeResult(m *ingest.Message, sentence string) any {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	pos.Set(model.KeyResult, sentence)
	return pos
}

func decodeExtension(pos *model.Position, buf *kit.Buf, endIndex int) {
	for buf.ReaderIndex() < endIndex {
		typ := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()
		switch typ {
		case 0x01:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
		case 0x02:
			pos.Set(model.KeyFuel, float64(buf.ReadUnsignedShort())/10.0)
		case 0x03:
			pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedShort())/10.0)
		case 0x56:
			buf.ReadUnsignedByte() // power level
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		case 0x61:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		case 0x69:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		case 0x80:
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())
		case 0x81:
			pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
		case 0x82:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
		case 0x83:
			pos.Set(model.KeyEngineLoad, buf.ReadUnsignedByte())
		case 0x84:
			pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedByte()-40)
		case 0x85:
			pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedShort())
		case 0x86:
			pos.Set("intakeTemp", buf.ReadUnsignedByte()-40)
		case 0x87:
			pos.Set("intakeFlow", buf.ReadUnsignedShort())
		case 0x88:
			pos.Set("intakePressure", buf.ReadUnsignedByte())
		case 0x89:
			pos.Set(model.KeyThrottle, buf.ReadUnsignedByte())
		case 0x8B:
			pos.Set(model.KeyVin, buf.ReadString(17))
		case 0x8C:
			pos.Set(model.KeyObdOdometer, buf.ReadUnsignedInt()*100)
		case 0x8D:
			pos.Set(model.KeyOdometerTrip, int64(buf.ReadUnsignedShort())*1000)
		case 0x8E:
			pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
		case 0xA0:
			pos.Set(model.KeyDtcs, strings.ReplaceAll(buf.ReadString(length), ",", " "))
		case 0xCC:
			pos.Set(model.KeyIccid, buf.ReadString(20))
		default:
			buf.Skip(length)
		}
	}
}

func (d *Decoder) decodeCoordinates(m *ingest.Message, pos *model.Position, ds *ingest.DeviceSession, buf *kit.Buf) {
	status := int64(buf.ReadInt())

	mdl := d.model(m, ds)

	pos.Set(model.KeyIgnition, kit.Check(status, 0))
	if mdl == "G1C Pro" {
		pos.Set(model.KeyMotion, kit.Check(status, 4))
	}
	pos.Set(model.KeyBlocked, kit.Check(status, 10))
	if mdl == "MV810G" || mdl == "MV710G" {
		pos.Set(model.KeyDoor, kit.Check(status, 16))
	}
	pos.Set(model.KeyCharge, kit.Check(status, 26))

	pos.Valid = kit.Check(status, 1)

	lat := float64(buf.ReadUnsignedInt()) / 1000000.0
	lon := float64(buf.ReadUnsignedInt()) / 1000000.0

	if kit.Check(status, 2) {
		pos.SetLatitude(-lat)
	} else {
		pos.SetLatitude(lat)
	}
	if kit.Check(status, 3) {
		pos.SetLongitude(-lon)
	} else {
		pos.SetLongitude(lon)
	}
}

func decodeCustomDouble(buf *kit.Buf) float64 {
	b1 := int(buf.ReadSignedByte())
	b2 := buf.ReadUnsignedByte()
	sign := 1
	if b1 != 0 {
		sign = b1 / abs(b1)
	}
	return float64(sign) * (float64(abs(b1)) + float64(b2)/255.0)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

var macPairs = regexp.MustCompile(`(..)`)

func (d *Decoder) decodeLocation(m *ingest.Message, ds *ingest.DeviceSession, zone *time.Location, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	mdl := d.model(m, ds)

	decodeAlarm(pos, mdl, buf.ReadUnsignedInt())

	d.decodeCoordinates(m, pos, ds, buf)

	pos.Altitude = float64(buf.ReadShort())
	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)
	pos.Course = float64(buf.ReadUnsignedShort())
	pos.SetTime(readDate(buf, zone))

	if buf.ReadableBytes() == 20 {
		buf.Skip(4) // remaining battery and mileage
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/10.0)
		buf.ReadUnsignedInt() // area id
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		buf.Skip(3) // reserved
		return pos
	}

	network := model.NewNetwork()

	for buf.ReadableBytes() > 2 {
		subtype := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()
		endIndex := buf.ReaderIndex() + length
		switch subtype {
		case 0x01:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
		case 0x02:
			fuel := buf.ReadUnsignedShort()
			if kit.Check(int64(fuel), 15) {
				pos.Set(model.KeyFuel, kit.ToInt(fuel, 15))
			} else {
				pos.Set(model.KeyFuel, float64(fuel)/10.0)
			}
		case 0x06:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		case 0x0B:
			decodeLock(pos, buf, length)
		case 0x14:
			pos.Set("videoAlarm", buf.ReadUnsignedInt())
		case 0x25:
			pos.Set(model.KeyInput, buf.ReadUnsignedInt())
		case 0x2B, 0xA7:
			pos.Set(model.PrefixAdc+"1", float64(buf.ReadUnsignedShort())/100.0)
			pos.Set(model.PrefixAdc+"2", float64(buf.ReadUnsignedShort())/100.0)
		case 0x30:
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		case 0x31:
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		case 0x33:
			if length == 1 {
				pos.Set("mode", buf.ReadUnsignedByte())
			} else {
				stringValue := buf.ReadString(length)
				if strings.HasPrefix(stringValue, "*M00") {
					lockStatus := kit.Substring(stringValue, 8, 8+7)
					pos.Set(model.KeyBattery, float64(kit.Int(kit.Substring(lockStatus, 2, 5)))/100.0)
				}
			}
		case 0x51:
			if length == 2 || length == 16 {
				for i := 1; i <= length/2; i++ {
					value := buf.ReadUnsignedShort()
					if value != 0xffff {
						key := model.PrefixTemp + strconv.Itoa(i)
						if kit.Check(int64(value), 15) {
							pos.Set(key, float64(-kit.ToInt(value, 15))/10.0)
						} else {
							pos.Set(key, float64(value)/10.0)
						}
					}
				}
			}
		case 0x56:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte()*10)
			buf.ReadUnsignedByte() // reserved
		case 0x57:
			alarm := int64(buf.ReadUnsignedShort())
			alarmIf(pos, kit.Check(alarm, 8), model.AlarmAcceleration)
			alarmIf(pos, kit.Check(alarm, 9), model.AlarmBraking)
			alarmIf(pos, kit.Check(alarm, 10), model.AlarmCornering)
			buf.ReadUnsignedShort() // external switch state
			alarm = buf.ReadUnsignedInt()
			if mdl == "MV810G" || mdl == "MV710G" {
				alarmIf(pos, kit.Check(alarm, 16), model.AlarmDoor)
			}
		case 0x60:
			event := buf.ReadUnsignedShort()
			pos.Set(model.KeyEvent, event)
			if event >= 0x0061 && event <= 0x0066 {
				buf.Skip(6) // lock id
				pos.Set(model.KeyDriverUniqueID, buf.ReadString(8))
			}
		case 0x63:
			for i := 1; i <= length/11; i++ {
				prefix := "lock" + strconv.Itoa(i)
				pos.Set(prefix+"Id", buf.ReadSlice(6).HexDump())
				pos.Set(prefix+"Battery", float64(buf.ReadUnsignedShort())/1000.0)
				pos.Set(prefix+"Seal", buf.ReadUnsignedByte() == 0x31)
				buf.ReadUnsignedByte() // physical state
				buf.ReadUnsignedByte() // rssi
			}
		case 0x64, 0x65:
			buf.ReadUnsignedInt()  // alarm serial number
			buf.ReadUnsignedByte() // alarm status
			key := "dmsAlarm"
			if subtype == 0x64 {
				key = "adasAlarm"
			}
			pos.Set(key, buf.ReadUnsignedByte())
			if length >= 47 {
				buf.SetReaderIndex(endIndex - 16)
				pos.Set("alarmLabel", buf.ReadSlice(16).HexDump())
			}
		case 0x67:
			pos.Set("password", buf.ReadString(8))
		case 0x70:
			buf.ReadUnsignedInt()  // alarm serial number
			buf.ReadUnsignedByte() // alarm status
			switch buf.ReadUnsignedByte() {
			case 0x01:
				pos.AddAlarm(model.AlarmAcceleration)
			case 0x02:
				pos.AddAlarm(model.AlarmBraking)
			case 0x03:
				pos.AddAlarm(model.AlarmCornering)
			case 0x16:
				pos.AddAlarm(model.AlarmAccident)
			}
		case 0x68:
			pos.Set(model.KeyBatteryLevel, float64(buf.ReadUnsignedShort())/100.0)
		case 0x69:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		case 0x77:
			for buf.ReaderIndex() < endIndex {
				prefix := "tire" + strconv.Itoa(buf.ReadUnsignedByte())
				pos.Set(prefix+"SensorId", buf.ReadSlice(3).HexDump())
				pos.Set(prefix+"Pressure", float64(kit.ToInt(buf.ReadUnsignedShort(), 10))/40.0)
				pos.Set(prefix+"Temp", buf.ReadUnsignedByte()-50)
				pos.Set(prefix+"Status", buf.ReadUnsignedByte())
			}
		case 0x80:
			buf.ReadUnsignedByte() // content
			endIndex = buf.WriterIndex() - 2
			decodeExtension(pos, buf, endIndex)
		case 0x82:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
		case 0x91:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/10.0)
			pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())
			pos.Set(model.KeyThrottle, buf.ReadUnsignedByte()*100/255)
			pos.Set(model.KeyEngineLoad, buf.ReadUnsignedByte()*100/255)
			pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedByte()-40)
			buf.ReadUnsignedShort()
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())/100.0)
			buf.ReadUnsignedShort()
			buf.ReadUnsignedInt()
			buf.ReadUnsignedShort()
			pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedShort())/100.0)
		case 0x94:
			if length > 0 {
				pos.Set(model.KeyVin, buf.ReadString(length))
			}
		case 0xAC:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		case 0xBC:
			pos.Set("driver", kit.Trim(buf.ReadString(length)))
		case 0xBD:
			pos.Set(model.KeyDriverUniqueID, buf.ReadString(length))
		case 0xD0:
			if kit.Check(buf.ReadUnsignedInt(), 3) {
				pos.AddAlarm(model.AlarmVibration)
			}
		case 0xD3:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
		case 0xD4, 0xE1, 0xE9:
			switch {
			case length == 1:
				batteryLevel := buf.ReadUnsignedByte()
				if batteryLevel == 0xff {
					pos.Set(model.KeyCharge, true)
				} else {
					pos.Set(model.KeyBatteryLevel, batteryLevel)
				}
			case subtype == 0xE1 && length >= 12 && (length-4)%8 == 0:
				mcc := buf.ReadUnsignedShort()
				mnc := buf.ReadUnsignedShort()
				for buf.ReaderIndex() < endIndex {
					lac := buf.ReadUnsignedMedium()
					cid := buf.ReadUnsignedInt()
					network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, buf.ReadUnsignedByte()))
				}
			case subtype == 0xE1 && length == 2:
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
			default:
				pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(buf.ReadUnsignedInt(), 10))
			}
		case 0xD5:
			if length == 2 {
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
			} else {
				count := buf.ReadUnsignedByte()
				for i := 1; i <= count; i++ {
					prefix := "lock" + strconv.Itoa(i)
					pos.Set(prefix+"Id", buf.ReadSlice(5).HexDump())
					pos.Set(prefix+"Card", buf.ReadSlice(5).HexDump())
					pos.Set(prefix+"Battery", buf.ReadUnsignedByte())
					status := buf.ReadUnsignedShort()
					pos.Set(prefix+"Locked", !kit.Check(int64(status), 5))
				}
			}
		case 0xDA:
			buf.ReadUnsignedShort() // string cut count
			deviceStatus := int64(buf.ReadUnsignedByte())
			pos.Set("string", kit.Check(deviceStatus, 0))
			pos.Set(model.KeyMotion, kit.Check(deviceStatus, 2))
			pos.Set("cover", kit.Check(deviceStatus, 3))
		case 0xE2:
			if mdl != "DT800" {
				pos.Set(model.KeyFuel, float64(buf.ReadUnsignedInt())/10.0)
			}
		case 0xE3:
			buf.ReadUnsignedByte() // reserved
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		case 0xE4:
			if buf.ReadUnsignedByte() == 0 {
				pos.Set(model.KeyCharge, true)
			}
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		case 0xE5:
			if length == 1 {
				pos.Set(model.KeyMotion, buf.ReadUnsignedByte() == 1)
			}
		case 0xE6:
			if length >= 7 && kit.UTF8(buf.GetBytes(buf.ReaderIndex(), 7)) == "$OBD-RT" {
				decodeObdRt(pos, buf.ReadUTF8(length))
			} else if length >= 11 && length%11 == 0 {
				for buf.ReaderIndex() < endIndex {
					sensorIndex := strconv.Itoa(buf.ReadUnsignedByte())
					buf.Skip(6) // mac
					pos.Set(model.PrefixTemp+sensorIndex, decodeCustomDouble(buf))
					pos.Set("humidity"+sensorIndex, decodeCustomDouble(buf))
				}
			}
		case 0xE7:
			if length == 8 {
				alarm := int64(buf.ReadUnsignedShort())
				alarmIf(pos, kit.Check(alarm, 0), model.AlarmVibration)
				alarmIf(pos, kit.Between(alarm, 1, 4) != 0, model.AlarmSos)
			}
		case 0xE8:
			if jcModels[mdl] {
				decodeJimiExtension(pos, buf, endIndex)
				buf.SetReaderIndex(endIndex)
			} else {
				pos.Set("lockStatus", buf.ReadUnsignedMedium())
			}
		case 0xEA:
			if length > 2 {
				buf.ReadUnsignedByte() // extended info type
				for buf.ReaderIndex() < endIndex {
					extendedType := buf.ReadUnsignedByte()
					extendedLength := buf.ReadUnsignedByte()
					extendedEndIndex := buf.ReaderIndex() + extendedLength
					switch extendedType {
					case 0x11:
						pos.Set("externalAlarms", buf.ReadUnsignedShort())
						pos.Set("alarmThresholdType", buf.ReadUnsignedByte())
						buf.ReadUnsignedInt() // upper threshold
						buf.ReadUnsignedInt() // current value
						buf.ReadUnsignedInt() // lower threshold
					case 0x13:
						pos.Set("externalIlluminance", buf.ReadUnsignedShort())
					case 0x14:
						pos.Set("externalAirPressure", buf.ReadUnsignedShort())
					case 0x15:
						pos.Set("externalHumidity", float64(buf.ReadUnsignedShort())/10.0)
					case 0x16:
						pos.Set("externalTemp", float64(buf.ReadUnsignedShort())/10.0-50)
					}
					buf.SetReaderIndex(extendedEndIndex)
				}
			}
		case 0xEB:
			decodeEB(pos, network, buf, length, endIndex)
		case 0xED:
			pos.Set(model.KeyCard, kit.Trim(buf.ReadString(length)))
		case 0xEE:
			if length == 6 {
				pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
				pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			}
		case 0xF1:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedInt())/1000.0)
		case 0xF3:
			decodeF3(pos, buf, endIndex)
		case 0xEC, 0xF4:
			for buf.ReaderIndex() < endIndex {
				mac := macPairs.ReplaceAllString(buf.ReadSlice(6).HexDump(), "$1:")
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(
					kit.Substring(mac, 0, kit.Len(mac)-1), int(buf.ReadSignedByte())))
			}
		case 0xF5:
			if length == 2 {
				pos.Set("illuminance", buf.ReadUnsignedShort())
			}
		case 0xF6:
			switch length {
			case 2:
				pos.Set("airPressure", buf.ReadUnsignedShort())
			case 8:
				pos.Set("imei", kit.SubstringFrom(buf.ReadSlice(length).HexDump(), 1))
			default:
				event := buf.ReadUnsignedByte()
				pos.Set(model.KeyEvent, event)
				if event == 2 {
					pos.Set(model.KeyMotion, true)
				}
				fieldMask := int64(buf.ReadUnsignedByte())
				if kit.Check(fieldMask, 0) {
					pos.Set("lightSensor", buf.ReadUnsignedShort())
				}
				if kit.Check(fieldMask, 1) {
					pos.Set(model.PrefixTemp+"1", float64(buf.ReadShort())/10.0)
				}
				if kit.Check(fieldMask, 2) {
					pos.Set(model.KeyHumidity, float64(buf.ReadShort())/10.0)
				}
			}
		case 0xF7:
			if length == 2 {
				pos.Set(model.KeyHumidity, float64(buf.ReadUnsignedShort())/10.0)
			} else {
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedInt())/1000.0)
				if length >= 5 {
					batteryStatus := buf.ReadUnsignedByte()
					if batteryStatus == 2 || batteryStatus == 3 {
						pos.Set(model.KeyCharge, true)
					}
				}
				if length >= 6 {
					pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
				}
			}
		case 0xF8:
			if mdl == "C5" || mdl == "C5L" {
				pos.Set(model.KeySteps, buf.ReadUnsignedShort())
			} else {
				pos.Set(model.PrefixTemp+"2", float64(buf.ReadUnsignedShort())/10.0-50)
			}
		case 0xFB:
			pos.Set("container", buf.ReadString(length))
		case 0xFC:
			pos.Set(model.KeyGeofence, buf.ReadUnsignedByte())
		case 0xFE:
			switch length {
			case 1:
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			case 2:
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
			case 4:
				pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
			default:
				if buf.ReadUnsignedByte() == 0x7C {
					for buf.ReaderIndex() < endIndex {
						extendedType := buf.ReadUnsignedByte()
						extendedLength := buf.ReadUnsignedByte()
						if extendedType == 0x01 {
							alarms := buf.ReadUnsignedInt()
							alarmIf(pos, kit.Check(alarms, 0), model.AlarmAcceleration)
							alarmIf(pos, kit.Check(alarms, 1), model.AlarmBraking)
							alarmIf(pos, kit.Check(alarms, 2), model.AlarmCornering)
							alarmIf(pos, kit.Check(alarms, 3), model.AlarmAccident)
							alarmIf(pos, kit.Check(alarms, 4), model.AlarmTampering)
						} else {
							buf.Skip(extendedLength)
						}
					}
				}
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			}
		}
		buf.SetReaderIndex(endIndex)
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}
	return pos
}

func decodeLock(pos *model.Position, buf *kit.Buf, length int) {
	pos.Set("lockCommand", buf.ReadUnsignedByte())
	if length >= 5 && length <= 6 {
		pos.Set("lockCard", buf.ReadUnsignedInt())
	} else if length >= 7 {
		pos.Set("lockPassword", buf.ReadString(6))
	}
	if length%2 == 0 {
		pos.Set("unlockResult", buf.ReadUnsignedByte())
	}
}

func decodeJimiExtension(pos *model.Position, buf *kit.Buf, endIndex int) {
	switch buf.ReadUnsignedShort() {
	case 0x2002:
		buf.ReadUnsignedByte() // packet length
		pos.Set("uploadMode", buf.ReadUnsignedByte())
		pos.Set("buffered", buf.ReadUnsignedByte() == 1)
	case 0x2003:
		buf.ReadUnsignedByte() // packet length
		pos.Set(model.KeyCharge, buf.ReadUnsignedByte() == 1)
		batteryStatus := buf.ReadUnsignedByte()
		if batteryStatus >= 1 && batteryStatus <= 6 {
			pos.Set(model.KeyBatteryLevel, batteryStatus*100/6)
		}
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
	case 0x2005:
		buf.ReadUnsignedByte() // packet length
		for buf.ReaderIndex() < endIndex {
			statusID := buf.ReadUnsignedShort()
			statusLength := buf.ReadUnsignedByte()
			statusEndIndex := buf.ReaderIndex() + statusLength
			switch statusID {
			case 0x0001:
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
			case 0x0002:
				pos.Set("dataUsage", buf.ReadUnsignedInt())
			case 0x0003:
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			case 0x0004:
				pos.Set(model.KeyCharge, buf.ReadUnsignedByte() == 0)
			case 0x0005:
				pos.Set(model.KeyIccid, kit.Substring(buf.ReadSlice(10).HexDump(), 0, 19))
			case 0x0007:
				pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShort())/10.0)
			case 0x2001:
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
			}
			buf.SetReaderIndex(statusEndIndex)
		}
	case 0x0001:
		pos.AddAlarm(model.AlarmFault)
	case 0x0400, 0x0412:
		pos.AddAlarm(model.AlarmAcceleration)
	case 0x0401, 0x0413:
		pos.AddAlarm(model.AlarmBraking)
	case 0x0402, 0x0414:
		pos.AddAlarm(model.AlarmCornering)
	case 0x0403:
		pos.AddAlarm(model.AlarmOverspeed)
	case 0x0405, 0x0416, 0x0417:
		pos.AddAlarm(model.AlarmAccident)
	case 0x0406:
		pos.AddAlarm(model.AlarmVibration)
	case 0x0407:
		pos.AddAlarm(model.AlarmTow)
	case 0x0408:
		pos.AddAlarm(model.AlarmGeofenceEnter)
	case 0x0409:
		pos.AddAlarm(model.AlarmGeofenceExit)
	case 0x040C:
		pos.AddAlarm(model.AlarmDoor)
	case 0x0415:
		pos.AddAlarm(model.AlarmLaneChange)
	case 0x0C01:
		pos.AddAlarm(model.AlarmSos)
	case 0x0C02:
		pos.AddAlarm(model.AlarmLowPower)
	case 0x0C03:
		pos.Set(model.KeyIgnition, true)
	case 0x0C04:
		pos.Set(model.KeyIgnition, false)
	case 0x0C05:
		pos.AddAlarm(model.AlarmGeneral)
	case 0x0C0E:
		pos.AddAlarm(model.AlarmPowerCut)
	case 0x0C0F:
		pos.AddAlarm(model.AlarmLowBattery)
	case 0x0C10:
		pos.AddAlarm(model.AlarmPowerOff)
	case 0x0C12:
		pos.AddAlarm(model.AlarmTampering)
	}
}

func decodeEB(pos *model.Position, network *model.Network, buf *kit.Buf, length, endIndex int) {
	if buf.GetUnsignedShort(buf.ReaderIndex()) > 200 && (length-3)%5 == 0 {
		mcc := buf.ReadUnsignedShort()
		mnc := buf.ReadUnsignedByte()
		for buf.ReaderIndex() < endIndex {
			lac := buf.ReadUnsignedShort()
			cid := buf.ReadUnsignedShort()
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), buf.ReadUnsignedByte()))
		}
		return
	}
	if kit.IsPrintable(buf, length) {
		pos.Set("timezone", buf.ReadUTF8(length))
		return
	}
	for buf.ReaderIndex() < endIndex {
		extendedLength := buf.ReadUnsignedShort()
		extendedEndIndex := buf.ReaderIndex() + extendedLength
		switch buf.ReadUnsignedShort() {
		case 0x0001:
			pos.Set("fuel1", float64(buf.ReadUnsignedShort())/10.0)
			buf.ReadUnsignedByte() // unused
		case 0x0023:
			pos.Set("fuel2", kit.Double(buf.ReadString(6)))
		case 0x002D:
			if extendedLength == 6 {
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedInt())/1000.0)
			} else {
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
			}
		case 0x0089:
			alarm := buf.ReadUnsignedInt()
			alarmIf(pos, !kit.Check(alarm, 0), model.AlarmPowerOff)
			alarmIf(pos, !kit.Check(alarm, 12), model.AlarmRemoving)
		case 0x00B2:
			pos.Set(model.KeyIccid, kit.StripTrailing('f', buf.ReadSlice(10).HexDump()))
		case 0x00B9:
			buf.ReadUnsignedByte() // count
			wifi := kit.Split(buf.ReadString(extendedLength-3), ",")
			for i := 0; i < len(wifi)/2; i++ {
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(wifi[i*2], kit.Int(wifi[i*2+1])))
			}
		case 0x00C5:
			alarm := buf.ReadUnsignedInt()
			alarmIf(pos, !kit.Check(alarm, 6), model.AlarmVibration)
			switch kit.Between(alarm, 23, 25) {
			case 0:
				pos.Set(model.KeyMotion, true)
			case 1:
				pos.Set(model.KeyMotion, false)
			}
		case 0x00C6:
			batteryAlarm := buf.ReadUnsignedByte()
			if batteryAlarm == 0x03 || batteryAlarm == 0x04 {
				pos.Set(model.KeyAlarm, model.AlarmLowBattery)
			}
			pos.Set("batteryAlarm", batteryAlarm)
		case 0x00CE:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		case 0x00D8:
			mcc := buf.ReadUnsignedShort()
			mnc := buf.ReadUnsignedByte()
			lac := buf.ReadUnsignedShort()
			network.AddCellTower(model.NewCellTower(mcc, mnc, lac, buf.ReadUnsignedInt()))
		case 0x00A8, 0x00E1:
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		}
		buf.SetReaderIndex(extendedEndIndex)
	}
}

func decodeF3(pos *model.Position, buf *kit.Buf, endIndex int) {
	for buf.ReaderIndex() < endIndex {
		extendedType := buf.ReadUnsignedShort()
		extendedLength := buf.ReadUnsignedByte()
		switch extendedType {
		case 0x0002:
			pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedShort())/10.0)
		case 0x0003:
			pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
		case 0x0004:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
		case 0x0005:
			pos.Set(model.KeyObdOdometer, buf.ReadUnsignedInt()*100)
		case 0x0007:
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())/10.0)
		case 0x0008:
			pos.Set(model.KeyEngineLoad, float64(buf.ReadUnsignedShort())/10.0)
		case 0x0009:
			pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedShort()-40)
		case 0x000B:
			pos.Set("intakePressure", buf.ReadUnsignedShort())
		case 0x000C:
			pos.Set("intakeTemp", buf.ReadUnsignedShort()-40)
		case 0x000D:
			pos.Set("intakeFlow", buf.ReadUnsignedShort())
		case 0x000E:
			pos.Set(model.KeyThrottle, buf.ReadUnsignedShort()*100/255)
		case 0x0050:
			pos.Set(model.KeyVin, buf.ReadString(17))
		case 0x0051:
			if extendedLength > 0 {
				pos.Set("cvn", buf.ReadSlice(extendedLength).HexDump())
			}
		case 0x0052:
			if extendedLength > 0 {
				pos.Set("calid", buf.ReadString(extendedLength))
			}
		case 0x0100:
			pos.Set(model.KeyOdometerTrip, float64(buf.ReadUnsignedShort())/10.0)
		case 0x0102:
			pos.Set("tripFuel", float64(buf.ReadUnsignedShort())/10.0)
		case 0x0112:
			pos.Set("hardAccelerationCount", buf.ReadUnsignedShort())
		case 0x0113:
			pos.Set("hardDecelerationCount", buf.ReadUnsignedShort())
		case 0x0114:
			pos.Set("hardCorneringCount", buf.ReadUnsignedShort())
		default:
			buf.Skip(extendedLength)
		}
	}
}

func decodeLocation2(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, typ int) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	jt600.DecodeBinaryLocation(buf, pos)
	pos.Valid = typ != msgLocationReportBlind

	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)

	battery := buf.ReadUnsignedByte()
	if battery <= 100 {
		pos.Set(model.KeyBatteryLevel, battery)
	} else if battery == 0xAA || battery == 0xAB {
		pos.Set(model.KeyCharge, true)
	}

	cid := buf.ReadUnsignedInt()
	lac := buf.ReadUnsignedShort()
	if cid > 0 && lac > 0 {
		pos.Network = model.NetworkOf(jt600.CellFromCidLac(m, cid, lac))
	}

	product := buf.ReadUnsignedByte()
	status := buf.ReadUnsignedShort()
	alarm := int64(buf.ReadUnsignedShort())

	if product == 1 || product == 2 {
		alarmIf(pos, kit.Check(alarm, 0), model.AlarmLowPower)
	} else if product == 3 {
		pos.Set(model.KeyBlocked, kit.Check(int64(status), 5))
		alarmIf(pos, kit.Check(alarm, 0), model.AlarmOverspeed)
		alarmIf(pos, kit.Check(alarm, 1), model.AlarmLowPower)
		alarmIf(pos, kit.Check(alarm, 2), model.AlarmVibration)
		alarmIf(pos, kit.Check(alarm, 3), model.AlarmLowBattery)
		alarmIf(pos, kit.Check(alarm, 5), model.AlarmGeofenceEnter)
		alarmIf(pos, kit.Check(alarm, 6), model.AlarmGeofenceExit)
	}

	pos.Set(model.KeyStatus, status)

	for buf.ReadableBytes() > 2 {
		id := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()
		switch id {
		case 0x02:
			pos.Altitude = float64(buf.ReadShort())
		case 0x10:
			pos.Set("wakeSource", buf.ReadUnsignedByte())
		case 0x0A:
			if length == 3 {
				buf.ReadUnsignedShort() // mcc
				buf.ReadUnsignedByte()  // mnc
			} else {
				buf.Skip(length)
			}
		case 0x0B:
			decodeLock(pos, buf, length)
		case 0x0C:
			axis := func() int {
				v := buf.ReadUnsignedShort()
				if v > 0x8000 {
					v -= 0x10000
				}
				return v
			}
			x := axis()
			y := axis()
			z := axis()
			pos.Set("tilt", "["+strconv.Itoa(x)+","+strconv.Itoa(y)+","+strconv.Itoa(z)+"]")
		case 0xFC:
			pos.Set(model.KeyGeofence, buf.ReadUnsignedByte())
		default:
			buf.Skip(length)
		}
	}
	return pos
}

func (d *Decoder) decodeLocationBatch(m *ingest.Message, ds *ingest.DeviceSession, zone *time.Location, buf *kit.Buf, typ int) []*model.Position {
	positions := []*model.Position{}

	locationType := 0
	if typ == msgLocationBatch {
		buf.ReadUnsignedShort() // count
		locationType = buf.ReadUnsignedByte()
	}

	for buf.ReadableBytes() > 2 {
		var length int
		if typ == msgLocationBatch2 {
			length = buf.ReadUnsignedByte()
		} else {
			length = buf.ReadUnsignedShort()
		}
		pos := d.decodeLocation(m, ds, zone, buf.ReadSlice(length))
		if locationType > 0 {
			pos.Set(model.KeyArchive, true)
		}
		positions = append(positions, pos)
	}
	return positions
}

func (d *Decoder) decodeTransparent(m *ingest.Message, ds *ingest.DeviceSession, zone *time.Location, buf *kit.Buf) any {
	typ := buf.ReadUnsignedByte()

	newPosition := func() *model.Position {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		return pos
	}

	switch typ {
	case 0x40:
		pos := newPosition()
		m.LastLocation(pos, nil)
		data := kit.Trim(buf.ReadString(buf.ReadableBytes()))
		if strings.HasPrefix(data, "GTSL") {
			values := kit.Split(data, "|")
			if len(values) > 4 {
				pos.Set(model.KeyDriverUniqueID, values[4])
			}
		} else {
			pos.Set("data", data)
		}
		if pos.Attributes.Len() == 0 {
			return nil
		}
		return pos

	case 0x41:
		pos := newPosition()
		m.LastLocation(pos, nil)
		decodeObdRt(pos, kit.Trim(buf.ReadString(buf.ReadableBytes()-2)))
		return pos

	case 0xF0:
		pos := newPosition()
		t := readDate(buf, zone)
		if buf.ReadUnsignedByte() > 0 {
			pos.Set(model.KeyArchive, true)
		}
		buf.ReadUnsignedByte() // vehicle type

		switch buf.ReadUnsignedByte() {
		case 0x01:
			count := buf.ReadUnsignedByte()
			for i := 0; i < count; i++ {
				id := buf.ReadUnsignedShort()
				length := buf.ReadUnsignedByte()
				switch id {
				case 0x0102, 0x0528, 0x0546:
					pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
				case 0x0103:
					pos.Set(model.KeyFuel, float64(buf.ReadUnsignedInt())/100.0)
				case 0x0111:
					pos.Set("fuelTemp", buf.ReadUnsignedByte()-40)
				case 0x012E:
					pos.Set("oilLevel", float64(buf.ReadUnsignedShort())/10.0)
				case 0x052A:
					pos.Set(model.KeyFuel, float64(buf.ReadUnsignedShort())/100.0)
				case 0x0105, 0x052C:
					pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())/100.0)
				case 0x014A, 0x0537, 0x0538, 0x0539:
					pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())/100.0)
				case 0x052B:
					pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
				case 0x052D:
					pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedByte()-40)
				case 0x052E:
					pos.Set("airTemp", buf.ReadUnsignedByte()-40)
				case 0x0530:
					pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
				case 0x0535:
					pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedShort())/10.0)
				case 0x0536:
					pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
				case 0x053D:
					pos.Set("intakePressure", float64(buf.ReadUnsignedShort())/10.0)
				case 0x0544:
					pos.Set("liquidLevel", buf.ReadUnsignedByte())
				case 0x0547, 0x0548:
					pos.Set(model.KeyThrottle, buf.ReadUnsignedByte())
				case 0xFEEC:
					pos.Set(model.KeyVin, buf.ReadString(length))
				default:
					key := model.PrefixIo + strconv.Itoa(id)
					switch length {
					case 1:
						pos.Set(key, buf.ReadUnsignedByte())
					case 2:
						pos.Set(key, buf.ReadUnsignedShort())
					case 4:
						pos.Set(key, buf.ReadUnsignedInt())
					default:
						buf.Skip(length)
					}
				}
			}
			m.LastLocation(pos, &t)
			d.decodeCoordinates(m, pos, ds, buf)
			pos.SetTime(t)
		case 0x02:
			codes := []string{}
			count := buf.ReadUnsignedShort()
			for i := 0; i < count; i++ {
				buf.ReadUnsignedInt() // system id
				codeCount := buf.ReadUnsignedShort()
				for j := 0; j < codeCount; j++ {
					buf.ReadUnsignedInt() // dtc
					buf.ReadUnsignedInt() // status
					codes = append(codes, kit.Trim(buf.ReadString(buf.ReadUnsignedShort())))
				}
			}
			pos.Set(model.KeyDtcs, strings.Join(codes, " "))
			m.LastLocation(pos, &t)
			d.decodeCoordinates(m, pos, ds, buf)
			pos.SetTime(t)
		case 0x03:
			count := buf.ReadUnsignedByte()
			for i := 0; i < count; i++ {
				id := buf.ReadUnsignedByte()
				length := buf.ReadUnsignedByte()
				switch id {
				case 0x01:
					pos.AddAlarm(model.AlarmPowerRestored)
				case 0x02:
					pos.AddAlarm(model.AlarmPowerCut)
				case 0x1A:
					pos.AddAlarm(model.AlarmAcceleration)
				case 0x1B:
					pos.AddAlarm(model.AlarmBraking)
				case 0x1C:
					pos.AddAlarm(model.AlarmCornering)
				case 0x1D, 0x1E, 0x1F:
					pos.AddAlarm(model.AlarmLaneChange)
				case 0x23:
					pos.AddAlarm(model.AlarmFatigueDriving)
				case 0x26, 0x27, 0x28:
					pos.AddAlarm(model.AlarmAccident)
				case 0x31, 0x32:
					pos.AddAlarm(model.AlarmDoor)
				}
				buf.Skip(length)
			}
			m.LastLocation(pos, &t)
			d.decodeCoordinates(m, pos, ds, buf)
			pos.SetTime(t)
		case 0x04:
			tripProperty := buf.ReadUnsignedByte()
			pos.Set("tripNumber", buf.ReadUnsignedInt())
			buf.Skip(6) // start time
			if tripProperty == 0x02 {
				buf.Skip(6) // end time
				buf.Skip(8) // start location
				buf.Skip(8) // end location
				buf.Skip(1) // location flags
				pos.Set("idlingCount", buf.ReadUnsignedShort())
				pos.Set("idlingTime", buf.ReadUnsignedShort())
				pos.Set(model.KeyOdometerTrip, int64(buf.ReadUnsignedShort())*100)
				pos.Set("tripFuel", float64(buf.ReadUnsignedShort())/100.0)
			}
			m.LastLocation(pos, &t)
		case 0x0B:
			if buf.ReadUnsignedByte() > 0 {
				pos.Set(model.KeyVin, buf.ReadString(17))
			}
			m.LastLocation(pos, &t)
		case 0x15:
			event := int(buf.ReadInt())
			switch event {
			case 51:
				pos.AddAlarm(model.AlarmAcceleration)
			case 52:
				pos.AddAlarm(model.AlarmBraking)
			case 53:
				pos.AddAlarm(model.AlarmCornering)
			case 54:
				pos.AddAlarm(model.AlarmLaneChange)
			case 56:
				pos.AddAlarm(model.AlarmAccident)
			default:
				pos.Set(model.KeyEvent, event)
			}
			m.LastLocation(pos, &t)
		default:
			return nil
		}
		return pos

	case 0xFF:
		pos := newPosition()
		pos.Valid = true
		pos.SetTime(readDate(buf, zone))
		pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
		pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		pos.Altitude = float64(buf.ReadShort())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)
		pos.Course = float64(buf.ReadUnsignedShort())
		return pos

	case 0xF3:
		pos := newPosition()
		for buf.ReadableBytes() > 4 {
			subtype := buf.ReadUnsignedShort()
			length := buf.ReadUnsignedShort()
			endIndex := buf.ReaderIndex() + length
			switch subtype {
			case 0x0002:
				pos.Set("collision", float64(buf.ReadUnsignedShort())/256.0/100.0)
			case 0x0006:
				pos.DeviceTime = model.NewTime(readDate(buf, zone))
			}
			buf.SetReaderIndex(endIndex)
		}
		if pos.DeviceTime.IsZero() {
			m.LastLocation(pos, nil)
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}
		return pos
	}
	return nil
}

// Encoder is Jt808ProtocolEncoder; it formats messages with the connection's decoder.
type Encoder struct {
	Decoder *Decoder
	// ModelOverride is setModelOverride ("" for none).
	ModelOverride string
}

func commandIntDefault(c *model.Command, key string, def int) int {
	if !c.Attributes.Has(key) {
		return def
	}
	return kit.CommandInt(c, key)
}

func commandData(c *model.Command) string {
	s := kit.CommandString(c, model.CommandKeyData)
	if s == nil {
		kit.Throw("NullPointerException: data")
	}
	return *s
}

func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	decoder := e.Decoder
	alternative := false
	if v := m.LookupBool(c.DeviceID, m.Protocol()+".alternative", false, true); v != nil {
		alternative = *v
	}

	idLength := 6
	if decoder.ProtocolVersion() != nil {
		idLength = 10
	}
	id := EncodeID(m.UniqueID(c.DeviceID), idLength)

	mdl := e.ModelOverride
	if mdl == "" {
		device, err := m.Device(c.DeviceID)
		if err != nil {
			panic(err)
		}
		if device != nil && device.Model != nil {
			mdl = *device.Model
		}
	}

	data := kit.Empty()
	switch c.Type {
	case model.CommandCustom:
		switch {
		case mdl == "AL300" || mdl == "GL100" || mdl == "VL300":
			text := commandData(c)
			data.WriteByte8(1)    // number of parameters
			data.WriteInt(0xF030) // AT command transparent transmission
			data.WriteByte8(kit.Len(text))
			data.WriteASCII(text)
			return decoder.FormatMessage(msgConfigurationParameters, id, false, data)
		case mdl == "BSJ" || mdl == "C5" || mdl == "C5L":
			data.WriteByte8(1) // flag
			data.WriteBytes(gbkEncode(commandData(c)))
			return decoder.FormatMessage(msgSendTextMessage, id, false, data)
		case strings.HasPrefix(mdl, "JC"):
			data.WriteByte8(0xF0) // online command
			data.WriteASCII(commandData(c))
			return decoder.FormatMessage(msgTransparentDownlink, id, false, data)
		default:
			return kit.NewBuf(kit.ParseHex(commandData(c)))
		}
	case model.CommandRebootDevice:
		data.WriteByte8(1)    // number of parameters
		data.WriteByte8(0x23) // parameter id
		data.WriteByte8(1)    // parameter value length
		data.WriteByte8(0x03) // restart
		return decoder.FormatMessage(msgParameterSetting, id, false, data)
	case model.CommandPositionPeriodic:
		data.WriteByte8(1)    // number of parameters
		data.WriteByte8(0x06) // parameter id
		data.WriteByte8(4)    // parameter value length
		data.WriteInt(int64(int32(kit.CommandInt(c, model.CommandKeyFrequency))))
		return decoder.FormatMessage(msgParameterSetting, id, false, data)
	case model.CommandAlarmArm, model.CommandAlarmDisarm:
		data.WriteByte8(1)    // number of parameters
		data.WriteByte8(0x24) // parameter id
		username := "user"
		data.WriteByte8(1 + len(username)) // parameter value length
		if c.Type == model.CommandAlarmArm {
			data.WriteByte8(0x01)
		} else {
			data.WriteByte8(0x00)
		}
		data.WriteASCII(username)
		return decoder.FormatMessage(msgParameterSetting, id, false, data)
	case model.CommandEngineStop, model.CommandEngineResume:
		stop := c.Type == model.CommandEngineStop
		if alternative {
			if stop {
				data.WriteByte8(0x01)
			} else {
				data.WriteByte8(0x00)
			}
			data.WriteBytes(kit.ParseHex(m.Now.In(m.Zone()).Format("060102150405")))
			return decoder.FormatMessage(msgOilControl, id, false, data)
		}
		switch mdl {
		case "VL300":
			if stop {
				data.WriteASCII("#0;1")
			} else {
				data.WriteASCII("#0;0")
			}
		case "W15L":
			if stop {
				data.WriteByte8(0x64)
			} else {
				data.WriteByte8(0x65)
			}
		default:
			if stop {
				data.WriteByte8(0xf0)
			} else {
				data.WriteByte8(0xf1)
			}
		}
		return decoder.FormatMessage(msgTerminalControl, id, false, data)
	case model.CommandVideoStart:
		cfg := m.Config()
		webURL, ok := cfg.Lookup("web.url")
		if !ok {
			kit.Throw("NullPointerException: web.url")
		}
		host := uriHost(webURL)
		port, err := cfg.Int("jt1078.port", 0)
		if err != nil {
			panic(err)
		}
		videoChannel := commandIntDefault(c, model.CommandKeyIndex, 1)
		data.WriteByte8(kit.Len(host))
		data.WriteASCII(host)
		data.WriteShort(port) // tcp port
		data.WriteShort(0)    // udp port
		data.WriteByte8(videoChannel)
		data.WriteByte8(1) // video only
		data.WriteByte8(0) // main stream
		return decoder.FormatMessage(msgVideoRequest, id, false, data)
	case model.CommandVideoStop:
		data.WriteByte8(commandIntDefault(c, model.CommandKeyIndex, 1))
		data.WriteByte8(0) // close audio/video transmission
		data.WriteByte8(0) // close both audio and video
		data.WriteByte8(0) // main stream
		return decoder.FormatMessage(msgVideoControl, id, false, data)
	}
	return nil
}
