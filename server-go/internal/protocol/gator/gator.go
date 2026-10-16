// Package gator is the Gator protocol (GatorProtocol): binary BCD reports framed by a
// length, identified by a pseudo IP address or an IMEI (M588), over TCP and UDP.
package gator

import (
	"fmt"
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP server (with the command encoder) and a
// UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "gator",
		DataCommands: protocol.DataCommands("gator"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 0, 0),
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

const (
	msgHeartbeat       = 0x21
	msgPositionRequest = 0x30
	msgOverspeedAlarm  = 0x3F
	msgResetMileage    = 0x6B
	msgRestoreOilDuct  = 0x38
	msgCloseOilDuct    = 0x39
	msgPositionData    = 0x80
	msgRollcallResp    = 0x81
	msgAlarmData       = 0x82
	msgBlindArea       = 0x8E
)

// DecodeID is GatorProtocolDecoder.decodeId: the pseudo IP address as a phone number.
func DecodeID(b1, b2, b3, b4 int) string {
	d1 := 30 + ((b1 >> 7) << 3) + ((b2 >> 7) << 2) + ((b3 >> 7) << 1) + (b4 >> 7)
	return fmt.Sprintf("%02d%02d%02d%02d%02d", d1, b1&0x7f, b2&0x7f, b3&0x7f, b4&0x7f)
}

func sendResponse(m *ingest.Message, msgType, checksum int) {
	response := kit.Empty()
	response.WriteShort(0x2424) // header
	response.WriteByte8(msgHeartbeat)
	response.WriteShort(5) // length
	response.WriteByte8(checksum)
	response.WriteByte8(msgType)
	response.WriteByte8(0) // subtype
	response.WriteByte8(kit.XOR(response.Bytes()[2:]))
	response.WriteByte8(0x0D)
	m.Send(response)
}

var digits = regexp.MustCompile(`^\d+$`)

func alarmIf(pos *model.Position, cond bool, alarm string) {
	if cond {
		pos.AddAlarm(alarm)
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	msgType := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length

	modelM588 := false
	var imei string
	if buf.ReadableBytes() > 8 {
		imei = kit.Substring(buf.Slice(buf.ReaderIndex(), 8).HexDump(), 0, 15)
		if digits.MatchString(imei) {
			number := kit.Long(kit.Substring(imei, 0, 14))
			if kit.Luhn(number) == kit.Long(kit.SubstringFrom(imei, 14)) {
				modelM588 = true
			}
		}
	}

	var ids []string
	if modelM588 {
		ids = []string{imei}
		buf.Skip(8)
	} else {
		b1 := buf.ReadUnsignedByte()
		b2 := buf.ReadUnsignedByte()
		b3 := buf.ReadUnsignedByte()
		b4 := buf.ReadUnsignedByte()
		id := DecodeID(b1, b2, b3, b4)
		ids = []string{"1" + id, id}
	}

	sendResponse(m, msgType, int(buf.GetByte(buf.WriterIndex()-2)))

	if msgType != msgPositionData && msgType != msgRollcallResp && msgType != msgAlarmData && msgType != msgBlindArea {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(ids...)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	year := kit.ReadBCD(buf, 2)
	if year < 100 {
		year += 2000 // DateBuilder.setYear
	}
	month := kit.ReadBCD(buf, 2)
	day := kit.ReadBCD(buf, 2)
	hour := kit.ReadBCD(buf, 2)
	minute := kit.ReadBCD(buf, 2)
	second := kit.ReadBCD(buf, 2)
	pos.SetTime(calendarDate(year, month, day, hour, minute, second))

	pos.SetLatitude(kit.ReadBCDCoordinate(buf))
	pos.SetLongitude(kit.ReadBCDCoordinate(buf))
	pos.Speed = units.KnotsFromKph(float64(kit.ReadBCD(buf, 4)))
	pos.Course = float64(kit.ReadBCD(buf, 4))

	flags := buf.ReadUnsignedByte()
	pos.Valid = flags&0x80 != 0
	pos.Set(model.KeySatellites, flags&0x0f)

	pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
	pos.Set("key", buf.ReadUnsignedByte())

	adc1 := float64(buf.ReadUnsignedByte())
	pos.Set(model.PrefixAdc+"1", adc1+float64(buf.ReadUnsignedByte())/100.0)
	adc2 := float64(buf.ReadUnsignedByte())
	pos.Set(model.PrefixAdc+"2", adc2+float64(buf.ReadUnsignedByte())/100.0)

	pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())

	if modelM588 && buf.ReadableBytes() >= 5+2 {
		buf.ReadUnsignedShort()
		buf.ReadUnsignedShort()
		alarm := int64(buf.ReadUnsignedByte())
		alarmIf(pos, kit.Check(alarm, 0), model.AlarmAcceleration)
		alarmIf(pos, kit.Check(alarm, 1), model.AlarmBraking)
		alarmIf(pos, kit.Check(alarm, 2), model.AlarmCornering)
	}

	if msgType == msgAlarmData {
		alarm1 := int64(buf.ReadUnsignedByte())
		alarmIf(pos, kit.Check(alarm1, 0), model.AlarmBraking)
		alarmIf(pos, kit.Check(alarm1, 5), model.AlarmAcceleration)
		alarm2 := int64(buf.ReadUnsignedByte())
		alarmIf(pos, kit.Check(alarm2, 1), model.AlarmOverspeed)
		alarmIf(pos, kit.Check(alarm2, 4), model.AlarmCornering)
	}

	return pos
}

// gregorianCutover is when java.util.GregorianCalendar switches from the Julian calendar.
var gregorianCutover = time.Date(1582, 10, 15, 0, 0, 0, 0, time.UTC)

// calendarDate is DateBuilder (a lenient GregorianCalendar in UTC) with these fields.
// Invalid BCD digits can make years before 1582, which Java counts in the Julian
// calendar; kit.DateBuilder is proleptic Gregorian.
func calendarDate(year, month, day, hour, minute, second int) time.Time {
	t := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if !t.Before(gregorianCutover) {
		return t
	}
	// Move the month overflow into the year, then count days from the Julian 1st.
	y, m := year+floorDiv(month-1, 12), floorMod(month-1, 12)+1
	first := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	shift := julianDayNumber(y, m, 1) - gregorianDayNumber(y, m, 1)
	return first.AddDate(0, 0, shift).Add(t.Sub(first))
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }

func julianDayNumber(y, m, d int) int {
	a := floorDiv(14-m, 12)
	yy := y + 4800 - a
	mm := m + 12*a - 3
	return d + floorDiv(153*mm+2, 5) + 365*yy + floorDiv(yy, 4) - 32083
}

func gregorianDayNumber(y, m, d int) int {
	a := floorDiv(14-m, 12)
	yy := y + 4800 - a
	mm := m + 12*a - 3
	return d + floorDiv(153*mm+2, 5) + 365*yy + floorDiv(yy, 4) - floorDiv(yy, 100) + floorDiv(yy, 400) - 32045
}

// encodeID is GatorProtocolEncoder.encodeId: the unique id as a pseudo IP address.
func encodeID(id string) *kit.Buf {
	buf := kit.Empty()
	firstDigit := kit.Int(kit.Substring(id, 1, 3)) - 30
	buf.WriteByte8(kit.Int(kit.Substring(id, 3, 5)) | (((firstDigit >> 3) & 1) << 7))
	buf.WriteByte8(kit.Int(kit.Substring(id, 5, 7)) | (((firstDigit >> 2) & 1) << 7))
	buf.WriteByte8(kit.Int(kit.Substring(id, 7, 9)) | (((firstDigit >> 1) & 1) << 7))
	buf.WriteByte8(kit.Int(kit.SubstringFrom(id, 9)) | ((firstDigit & 1) << 7))
	return buf
}

func encodeContent(uniqueID string, msgType int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0x24)
	buf.WriteByte8(0x24)
	buf.WriteByte8(msgType)
	buf.WriteByte8(0x00)
	length := 4 + 1 + 1
	if content != nil {
		length += content.ReadableBytes()
	}
	buf.WriteByte8(length)
	buf.WriteBuf(encodeID(uniqueID))
	if content != nil {
		buf.WriteBuf(content)
	}
	buf.WriteByte8(kit.XOR(buf.Bytes()))
	buf.WriteByte8(0x0D)
	return buf
}

func encode(m *ingest.Message, c *model.Command) any {
	return encodeWith(c, func() string { return m.UniqueID(c.DeviceID) })
}

// encodeWith is GatorProtocolEncoder.encodeCommand; uniqueID is getUniqueId(deviceId),
// called only for the commands it encodes.
func encodeWith(c *model.Command, uniqueID func() string) any {
	content := kit.Empty()
	switch c.Type {
	case model.CommandPositionSingle:
		return encodeContent(uniqueID(), msgPositionRequest, nil)
	case model.CommandEngineStop:
		return encodeContent(uniqueID(), msgCloseOilDuct, nil)
	case model.CommandEngineResume:
		return encodeContent(uniqueID(), msgRestoreOilDuct, nil)
	case model.CommandSetSpeedLimit:
		content.WriteByte8(kit.CommandInt(c, model.CommandKeyData))
		return encodeContent(uniqueID(), msgResetMileage, content)
	case model.CommandSetOdometer:
		content.WriteShort(kit.CommandInt(c, model.CommandKeyData))
		return encodeContent(uniqueID(), msgOverspeedAlarm, content)
	}
	return nil
}
