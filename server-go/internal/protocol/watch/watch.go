// Package watch is the Watch protocol (WatchProtocol): "[CS*id*len*content]" messages of
// children's watches (3G/SG and others), with escaped binary audio and images, over TCP.
package watch

import (
	"encoding/hex"
	"math"
	"regexp"
	"strconv"
	"strings"
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
		Name:         "watch",
		DataCommands: protocol.DataCommands("watch"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: d,
				Encoder: &Encoder{Decoder: d},
			}
		}}},
	}
}

// Framer is WatchFrameDecoder: a frame is a balanced [...] block; '}' escapes the next
// byte (01 '}', 02 '[', 03 ']', 04 ',', 05 '*').
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		brackets := 0
		endIndex := -1
		for i := buf.ReaderIndex(); i < buf.WriterIndex(); i++ {
			switch buf.GetByte(i) {
			case '[':
				brackets++
			case ']':
				brackets--
			}
			if brackets == 0 && i > buf.ReaderIndex() {
				endIndex = i + 1
				break
			}
		}

		if endIndex > 0 {
			frame := kit.Empty()
			for buf.ReaderIndex() < endIndex {
				b1 := buf.ReadSignedByte()
				if b1 == '}' {
					b2 := buf.ReadSignedByte()
					switch b2 {
					case 0x01:
						frame.WriteByte8('}')
					case 0x02:
						frame.WriteByte8('[')
					case 0x03:
						frame.WriteByte8(']')
					case 0x04:
						frame.WriteByte8(',')
					case 0x05:
						frame.WriteByte8('*')
					default:
						kit.Throw("IllegalArgumentException: %s", kit.Format("unexpected byte at %d: 0x%02x", buf.ReaderIndex()-1, int(b2)))
					}
				} else {
					frame.WriteByte8(int(b1))
				}
			}
			return frame
		}
		return nil
	}}
}

// Decoder is WatchProtocolDecoder. The encoder of the connection reads hasIndex and
// manufacturer.
type Decoder struct {
	hasIndex     bool
	manufacturer *string // null until a message is decoded
	media        *kit.Buf
}

var patternPosition = kit.NewPattern().
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([AV]),").   // validity
	Number(" *(-?d+.?d*),"). // latitude
	Expression("([NS])?,").
	Number(" *(-?d+.?d*),"). // longitude
	Expression("([EW])?,").
	Number("(d+.?d*),").   // speed
	Number("(d+.?d*),").   // course
	Number("(-?d+.?d*),"). // altitude
	Number("(d+),").       // satellites
	Number("(d+),").       // rssi
	Number("(d+),").       // battery
	Number("(d+),").       // steps
	Number("d+,").         // tumbles
	Number("(x+),").       // status
	Expression("(.*)").    // cell and wifi
	Compile()

func (d *Decoder) sendResponse(m *ingest.Message, id string, index *string, content string) {
	var response string
	if index != nil {
		response = kit.Format("[%s*%s*%s*%04x*%s]", *d.manufacturer, id, *index, kit.Len(content), content)
	} else {
		response = kit.Format("[%s*%s*%04x*%s]", *d.manufacturer, id, kit.Len(content), content)
	}
	m.Send(kit.NewBuf(kit.NettyBytes(response)))
}

func decodeAlarm(status int) string {
	s := int64(status)
	switch {
	case kit.Check(s, 0):
		return model.AlarmLowBattery
	case kit.Check(s, 1):
		return model.AlarmGeofenceExit
	case kit.Check(s, 2):
		return model.AlarmGeofenceEnter
	case kit.Check(s, 14):
		return model.AlarmPowerCut
	case kit.Check(s, 16):
		return model.AlarmSos
	case kit.Check(s, 17):
		return model.AlarmLowBattery
	case kit.Check(s, 18):
		return model.AlarmGeofenceExit
	case kit.Check(s, 19):
		return model.AlarmGeofenceEnter
	case kit.Check(s, 20):
		return model.AlarmRemoving
	case kit.Check(s, 21) || kit.Check(s, 22):
		return model.AlarmFallDown
	}
	return ""
}

func parseIntAuto(s string) int {
	if kit.ContainsHex(s) {
		return kit.IntRadix(s, 16)
	}
	return kit.Int(s)
}

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, data string) *model.Position {
	parser := kit.NewParser(patternPosition, data)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))
	pos.Set(model.KeyBatteryLevel, parser.NextIntDefault(0))

	pos.Set(model.KeySteps, parser.NextIntDefault(0))

	status := parser.NextHexIntDefault(0)
	pos.AddAlarm(decodeAlarm(status))
	if kit.Check(int64(status), 4) {
		pos.Set(model.KeyMotion, true)
	}

	values := kit.Split(parser.Next(), ",")
	index := 0

	if len(values) < 4 || !kit.ContainsHex(values[index+3]) {
		network := model.NewNetwork()

		cellCount := kit.Int(values[index])
		index++
		if cellCount > 0 {
			index++ // timing advance
			mcc, mnc := 0, 0
			if values[index] != "" {
				mcc = kit.Int(values[index])
				index++
			}
			if values[index] != "" {
				mnc = kit.Int(values[index])
				index++
			}

			for range cellCount {
				lac := parseIntAuto(values[index])
				index++
				cid := parseIntAuto(values[index])
				index++
				rssi := values[index]
				index++
				if rssi != "" {
					network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), kit.Int(rssi)))
				} else {
					network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
				}
			}
		}

		if index < len(values) && values[index] != "" {
			wifiCount := kit.Int(values[index])
			index++

			for range wifiCount {
				index++ // wifi name
				macAddress := values[index]
				index++
				rssi := values[index]
				index++
				if macAddress != "" && macAddress != "0" && rssi != "" {
					network.AddWifiAccessPoint(model.NewWifiAccessPoint(macAddress, kit.Int(rssi)))
				}
			}
		}

		if network.CellTowers != nil || network.WifiAccessPoints != nil {
			pos.Network = network
		}
	}

	return pos
}

var xdigits = regexp.MustCompile(`^[0-9a-fA-F]+$`)

func (d *Decoder) newPosition(m *ingest.Message, ds *ingest.DeviceSession) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	return pos
}

func isAny(typ string, names ...string) bool {
	for _, n := range names {
		if strings.EqualFold(typ, n) {
			return true
		}
	}
	return false
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(1) // '[' header
	manufacturer := buf.ReadString(2)
	d.manufacturer = &manufacturer
	buf.Skip(1) // '*' delimiter

	idIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '*')
	id := buf.ReadString(idIndex - buf.ReaderIndex())
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	buf.Skip(1) // '*' delimiter

	var index *string
	contentIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '*')
	if contentIndex+5 < buf.WriterIndex() && buf.GetByte(contentIndex+5) == '*' &&
		xdigits.MatchString(buf.GetLatin1(contentIndex+1, 4)) {
		indexLength := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '*') - buf.ReaderIndex()
		d.hasIndex = true
		s := buf.ReadString(indexLength)
		index = &s
		buf.Skip(1) // '*' delimiter
	}

	buf.Skip(4) // length
	buf.Skip(1) // '*' delimiter

	buf.SetWriterIndex(buf.WriterIndex() - 1) // ']' ignore ending

	contentIndex = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')
	if contentIndex < 0 {
		contentIndex = buf.WriterIndex()
	}

	typ := buf.ReadString(contentIndex - buf.ReaderIndex())

	if contentIndex < buf.WriterIndex() {
		buf.SetReaderIndex(contentIndex + 1)
	}

	switch {
	case typ == "INIT":
		d.sendResponse(m, id, index, "INIT,1")

	case typ == "LK":
		d.sendResponse(m, id, index, "LK")
		if buf.IsReadable() {
			values := kit.Split(kit.Latin1(buf.Bytes()), ",")
			if len(values) >= 3 {
				pos := d.newPosition(m, ds)
				m.LastLocation(pos, nil)
				pos.Set(model.KeyBatteryLevel, kit.Int(values[2]))
				pos.Set(model.KeySteps, kit.Int(values[0]))
				return pos
			}
		}

	case strings.HasPrefix(typ, "UD") || strings.HasPrefix(typ, "AL") || strings.HasPrefix(typ, "WT"):
		pos := decodePosition(m, ds, kit.Latin1(buf.Bytes()))
		if strings.HasPrefix(typ, "AL") {
			if pos != nil {
				if _, ok := pos.Attributes.Get(model.KeyAlarm); !ok {
					pos.AddAlarm(model.AlarmGeneral)
				}
			}
			d.sendResponse(m, id, index, "AL")
		}
		if pos == nil {
			return nil
		}
		return pos

	case typ == "TKQ" || typ == "TKQ2":
		d.sendResponse(m, id, index, typ)

	case isAny(typ, "PULSE", "HEART", "BLOOD", "BPHRT", "TEMP", "btemp2", "oxygen"):
		if buf.IsReadable() {
			pos := d.newPosition(m, ds)
			now := m.Now
			m.LastLocation(pos, &now)

			values := kit.Split(kit.Latin1(buf.Bytes()), ",")
			valueIndex := 0

			switch {
			case strings.EqualFold(typ, "TEMP"):
				pos.Set(model.PrefixTemp+"1", kit.Double(values[valueIndex]))
			case strings.EqualFold(typ, "btemp2"):
				v := kit.Int(values[valueIndex])
				valueIndex++
				if v > 0 {
					pos.Set(model.PrefixTemp+"1", kit.Double(values[valueIndex]))
				}
			case strings.EqualFold(typ, "oxygen"):
				valueIndex++
				pos.Set("bloodOxygen", kit.Int(values[valueIndex]))
			default:
				if strings.EqualFold(typ, "BPHRT") || strings.EqualFold(typ, "BLOOD") {
					pos.Set("pressureHigh", values[valueIndex])
					valueIndex++
					pos.Set("pressureLow", values[valueIndex])
					valueIndex++
				}
				if valueIndex <= len(values)-1 {
					pos.Set(model.KeyHeartRate, kit.Int(values[valueIndex]))
				}
			}
			return pos
		}

	case typ == "img":
		pos := d.newPosition(m, ds)
		m.LastLocation(pos, nil)

		timeIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')
		buf.SetReaderIndex(timeIndex + 12 + 2)
		pos.Set(model.KeyImage, m.WriteMedia(id, "jpg", buf.Bytes()))
		return pos

	case typ == "JXTK":
		dataIndex := kit.IndexOfCount(buf, buf.ReaderIndex(), buf.WriterIndex(), ',', 4) + 1
		values := kit.Split(buf.ReadString(dataIndex-buf.ReaderIndex()), ",")

		current := kit.Int(values[2])
		total := kit.Int(values[3])

		if d.media == nil {
			d.media = m.NewMediaBuffer()
		}
		d.media.WriteBuf(buf)
		buf.Skip(buf.ReadableBytes())

		d.sendResponse(m, id, index, "JXTKR,1")

		if current < total {
			return nil
		}
		pos := d.newPosition(m, ds)
		m.LastLocation(pos, nil)
		media := d.media
		d.media = nil
		pos.Set(model.KeyAudio, m.WriteMedia(id, "amr", media.Bytes()))
		return pos

	case typ == "TK" || typ == "TK2":
		if buf.ReadableBytes() == 1 {
			return nil
		}
		pos := d.newPosition(m, ds)
		m.LastLocation(pos, nil)
		pos.Set(model.KeyAudio, m.WriteMedia(id, "amr", buf.Bytes()))
		return pos
	}

	return nil
}

// Encoder is WatchProtocolEncoder; Decoder is the connection's decoder (nil without a
// channel).
type Encoder struct {
	Decoder *Decoder
}

// formatValue is the encoder's ValueFormatter.
func formatValue(m *ingest.Message, key string, value any) (string, bool) {
	switch key {
	case model.CommandKeyTimezone:
		id, ok := value.(string)
		if !ok {
			kit.Throw("ClassCastException: %T is not a String", value)
		}
		offset := float64(javatime.RawOffset(javatime.TimeZone(id), m.Now)*1000) / 3600000.0
		return formatOffset(offset), true
	case model.CommandKeyMessage:
		if value == nil {
			kit.Throw("NullPointerException: message")
		}
		units := utf16.Encode([]rune(model.JavaToString(value)))
		b := make([]byte, 0, 2*len(units))
		for _, u := range units {
			b = append(b, byte(u>>8), byte(u))
		}
		return hex.EncodeToString(b), true
	case model.CommandKeyEnable:
		v, ok := value.(bool)
		if !ok {
			kit.Throw("ClassCastException: %T is not a Boolean", value)
		}
		if v {
			return "1", true
		}
		return "0", true
	}
	return "", false
}

// formatOffset is new DecimalFormat("+#.##;-#.##").format(offset).
func formatOffset(offset float64) string {
	sign := "+"
	if offset < 0 {
		sign = "-"
	}
	r := math.RoundToEven(math.Abs(offset)*100) / 100
	return sign + strconv.FormatFloat(r, 'f', -1, 64)
}

func (e *Encoder) formatTextCommand(m *ingest.Message, c *model.Command, format string, keys ...string) *kit.Buf {
	content := kit.FormatCommandWith(c, m.UniqueID(c.DeviceID), format, func(key string, value any) (string, bool) {
		return formatValue(m, key, value)
	}, keys...)
	return e.formatBinaryCommand(m, c, "", kit.NewBuf(kit.NettyBytes(content)))
}

func (e *Encoder) formatBinaryCommand(m *ingest.Message, c *model.Command, textPrefix string, data *kit.Buf) *kit.Buf {
	hasIndex := false
	manufacturer := "CS"
	if e.Decoder != nil {
		hasIndex = e.Decoder.hasIndex
		if e.Decoder.manufacturer == nil {
			kit.Throw("NullPointerException: manufacturer")
		}
		manufacturer = *e.Decoder.manufacturer
		if manufacturer == "3G" {
			manufacturer = "SG"
		}
	}

	buf := kit.Empty()
	buf.WriteByte8('[')
	buf.WriteASCII(manufacturer)
	buf.WriteByte8('*')
	buf.WriteASCII(m.UniqueID(c.DeviceID))
	buf.WriteByte8('*')
	if hasIndex {
		buf.WriteASCII("0001")
		buf.WriteByte8('*')
	}
	buf.WriteASCII(kit.Format("%04x", data.ReadableBytes()+kit.Len(textPrefix)))
	buf.WriteByte8('*')
	buf.WriteASCII(textPrefix)
	buf.WriteBuf(data)
	buf.WriteByte8(']')
	return buf
}

var mapping = map[byte]byte{0x7d: 0x01, 0x5B: 0x02, 0x5D: 0x03, 0x2C: 0x04, 0x2A: 0x05}

func getBinaryData(c *model.Command) *kit.Buf {
	data := kit.CommandString(c, model.CommandKeyData)
	if data == nil {
		kit.Throw("NullPointerException: data")
	}
	encoded := kit.Empty()
	for _, b := range kit.ParseHex(*data) {
		if r, ok := mapping[b]; ok {
			encoded.WriteByte8(0x7D)
			encoded.WriteByte8(int(r))
		} else {
			encoded.WriteByte8(int(b))
		}
	}
	return encoded
}

// Encode is encodeCommand.
func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: format")
		}
		return e.formatTextCommand(m, c, *data)
	case model.CommandPositionSingle:
		return e.formatTextCommand(m, c, "CR")
	case model.CommandSosNumber:
		return e.formatTextCommand(m, c, "SOS%s,%s", model.CommandKeyIndex, model.CommandKeyPhone)
	case model.CommandAlarmSos:
		return e.formatTextCommand(m, c, "SOSSMS,%s", model.CommandKeyEnable)
	case model.CommandAlarmBattery:
		return e.formatTextCommand(m, c, "LOWBAT,%s", model.CommandKeyEnable)
	case model.CommandRebootDevice:
		return e.formatTextCommand(m, c, "RESET")
	case model.CommandPowerOff:
		return e.formatTextCommand(m, c, "POWEROFF")
	case model.CommandAlarmRemove:
		return e.formatTextCommand(m, c, "REMOVE,%s", model.CommandKeyEnable)
	case model.CommandSilenceTime:
		return e.formatTextCommand(m, c, "SILENCETIME,%s", model.CommandKeyData)
	case model.CommandAlarmClock:
		return e.formatTextCommand(m, c, "REMIND,%s", model.CommandKeyData)
	case model.CommandSetPhonebook:
		return e.formatTextCommand(m, c, "PHB,%s", model.CommandKeyData)
	case model.CommandMessage:
		return e.formatTextCommand(m, c, "MESSAGE,%s", model.CommandKeyMessage)
	case model.CommandVoiceMessage:
		return e.formatBinaryCommand(m, c, "TK,", getBinaryData(c))
	case model.CommandPositionPeriodic:
		return e.formatTextCommand(m, c, "UPLOAD,%s", model.CommandKeyFrequency)
	case model.CommandSetTimezone:
		return e.formatTextCommand(m, c, "LZ,%s,%s", model.CommandKeyLanguage, model.CommandKeyTimezone)
	case model.CommandSetIndicator:
		return e.formatTextCommand(m, c, "FLOWER,%s", model.CommandKeyData)
	}
	return nil
}
