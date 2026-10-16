// Package meiligao is the Meiligao protocol (MeiligaoProtocol): "$$" binary frames with a
// BCD device id and NMEA-like text payloads, over TCP and UDP; replies and commands are
// "@@" frames.
package meiligao

import (
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{Decoder: &Decoder{}, Encoder: ingest.CommandEncoderFunc(encode)}
			if framer {
				pl.Framer = Framer()
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "meiligao",
		DataCommands: protocol.DataCommands("meiligao"),
		Servers:      []ingest.Server{{NewPipeline: pipeline(true)}, {Datagram: true, NewPipeline: pipeline(false)}},
	}
}

// Framer is MeiligaoFrameDecoder: bytes before '$' are dropped; the length is at offset 2.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		for buf.IsReadable() && buf.GetUnsignedByte(buf.ReaderIndex()) != 0x24 {
			buf.Skip(1)
		}
		if buf.ReadableBytes() >= 4 {
			length := buf.GetUnsignedShort(buf.ReaderIndex() + 2)
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Number("(d+)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(d+)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").   // speed
	Number("(d+.?d*)?,").   // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	Expression(`[^\|]*`).
	GroupBegin().
	Number("|(d+.d+)?").    // hdop
	Number("|(-?d+.?d*)?"). // altitude
	Number("|(xxxx)?").     // state
	GroupBegin().
	Number("|(xxxx),(xxxx)"). // adc
	Number(",(xxxx)").Optional().
	Number(",(xxxx)").Optional().
	Number(",(xxxx)").Optional().
	Number(",(xxxx)").Optional().
	Number(",(xxxx)").Optional().
	Number(",(xxxx)").Optional().
	GroupBegin().
	Number("|x{16,20}"). // cell
	Number("|(xx)").     // rssi
	Number("|(x{8})").   // odometer
	GroupBegin().
	Number("|(xx)"). // satellites
	GroupBegin().
	Text("|").
	Expression("(.*)"). // driver
	GroupEndWith("?").
	GroupEndWith("?").
	Or().
	Number("|(d{1,9})"). // odometer
	GroupBegin().
	Number("|(x{5,})"). // rfid
	GroupEndWith("?").
	GroupEndWith("?").
	GroupEndWith("?").
	GroupEndWith("?").
	Any().
	Compile()

var patternRfid = kit.NewPattern().
	Number("|(dd)(dd)(dd),"). // time (hhmmss)
	Number("(dd)(dd)(dd),").  // date (ddmmyy)
	Number("(d+)(dd.d+),").   // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW])").
	Compile()

var patternObd = kit.NewPattern().
	Number("(d+.d+),").  // battery
	Number("(d+),").     // rpm
	Number("(d+),").     // speed
	Number("(d+.d+),").  // throttle
	Number("(d+.d+),").  // engine load
	Number("(-?d+),").   // coolant temp
	Number("(d+.d+),").  // instantaneous fuel
	Number("(d+.d+),").  // average fuel
	Number("(d+.d+),").  // driving range
	Number("(d+.?d*),"). // odometer
	Number("(d+.d+),").  // single fuel consumption
	Number("(d+.d+),").  // total fuel consumption
	Number("(d+),").     // error code count
	Number("(d+),").     // hard acceleration count
	Number("(d+)").      // hard brake count
	Compile()

var patternObdA = kit.NewPattern().
	Number("(d+),").    // total ignition
	Number("(d+.d+),"). // total driving time
	Number("(d+.d+),"). // total idling time
	Number("(d+),").    // average hot start time
	Number("(d+),").    // average speed
	Number("(d+),").    // history highest speed
	Number("(d+),").    // history highest rpm
	Number("(d+),").    // total hard acceleration
	Number("(d+)").     // total hard brake
	Compile()

// Message types.
const (
	msgHeartbeat           = 0x0001
	msgServer              = 0x0002
	msgLogin               = 0x5000
	msgLoginResponse       = 0x4000
	msgPosition            = 0x9955
	msgPositionLogged      = 0x9016
	msgAlarm               = 0x9999
	msgRfid                = 0x9966
	msgRetransmission      = 0x6688
	msgObdRt               = 0x9901
	msgObdRta              = 0x9902
	msgDtc                 = 0x9903
	msgTrackOnDemand       = 0x4101
	msgTrackByInterval     = 0x4102
	msgMovementAlarm       = 0x4106
	msgOutputControl1      = 0x4114
	msgOutputControl2      = 0x4115
	msgTimeZone            = 0x4132
	msgTakePhoto           = 0x4151
	msgUploadPhoto         = 0x0800
	msgUploadPhotoResponse = 0x8801
	msgDataPhoto           = 0x9988
	msgPositionImage       = 0x9977
	msgUploadComplete      = 0x0f80
	msgRebootGps           = 0x4902
)

// Decoder is MeiligaoProtocolDecoder; it holds the media buffer of a photo upload.
type Decoder struct {
	media *kit.Buf
}

func identify(m *ingest.Message, buf *kit.Buf) *ingest.DeviceSession {
	var b strings.Builder
	for i := 0; i < 7; i++ {
		v := buf.ReadUnsignedByte()
		d1 := (v & 0xf0) >> 4
		if d1 == 0xf {
			break
		}
		b.WriteString(strconv.Itoa(d1))
		d2 := v & 0x0f
		if d2 == 0xf {
			break
		}
		b.WriteString(strconv.Itoa(d2))
	}
	id := b.String()
	if len(id) == 14 {
		return m.Session(id, id+strconv.FormatInt(kit.Luhn(kit.Long(id)), 10))
	}
	return m.Session(id)
}

// frame builds an "@@" message: header, length, id, type, content, CRC and CRLF.
func frame(id []byte, typ int, content []byte) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8('@')
	buf.WriteByte8('@')
	buf.WriteShort(2 + 2 + len(id) + 2 + len(content) + 2 + 2)
	buf.WriteBytes(id)
	buf.WriteShort(typ)
	buf.WriteBytes(content)
	buf.WriteShort(kit.CRC16(kit.CRC16_CCITT_FALSE, buf.Bytes()))
	buf.WriteByte8('\r')
	buf.WriteByte8('\n')
	return buf
}

func sendResponse(m *ingest.Message, id *kit.Buf, typ int, content []byte) {
	m.Send(frame(id.Bytes(), typ, content))
}

func decodeAlarm(deviceModel string, value int) string {
	if deviceModel == "TK218" {
		switch value {
		case 0x01:
			return model.AlarmSos
		case 0x10:
			return model.AlarmLowBattery
		case 0x11:
			return model.AlarmOverspeed
		case 0x12:
			return model.AlarmMovement
		case 0x13:
			return model.AlarmGeofence
		case 0x60:
			return model.AlarmFatigueDriving
		case 0x71:
			return model.AlarmBraking
		case 0x72:
			return model.AlarmAcceleration
		case 0x73:
			return model.AlarmAccident
		case 0x74:
			return model.AlarmIdle
		}
		return ""
	}
	switch value {
	case 0x01:
		return model.AlarmSos
	case 0x10:
		return model.AlarmLowBattery
	case 0x11:
		return model.AlarmOverspeed
	case 0x12:
		return model.AlarmMovement
	case 0x13:
		return model.AlarmGeofenceEnter
	case 0x14:
		return model.AlarmAccident
	case 0x50:
		return model.AlarmPowerOff
	case 0x53:
		return model.AlarmGpsAntennaCut
	case 0x72:
		return model.AlarmBraking
	case 0x73:
		return model.AlarmAcceleration
	}
	return ""
}

func decodeRegular(pos *model.Position, sentence string) *model.Position {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	date := kit.NewDateBuilder(nil).SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	if parser.HasNext() {
		pos.Speed = parser.NextDoubleDefault(0)
	}
	if parser.HasNext() {
		pos.Course = parser.NextDoubleDefault(0)
	}

	date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())

	pos.Set(model.KeyHdop, parser.NextDouble())

	if parser.HasNext() {
		pos.Altitude = parser.NextDoubleDefault(0)
	}

	if parser.HasNext() {
		status := int64(parser.MustHexInt())
		for i := 1; i <= 5; i++ {
			pos.Set(model.PrefixOut+strconv.Itoa(i), kit.Check(status, i-1))
		}
		for i := 1; i <= 5; i++ {
			pos.Set(model.PrefixIn+strconv.Itoa(i), kit.Check(status, i-1+8))
		}
	}

	for i := 1; i <= 8; i++ {
		pos.Set(model.PrefixAdc+strconv.Itoa(i), parser.NextHexInt())
	}

	pos.Set(model.KeyRssi, parser.NextHexInt())
	pos.Set(model.KeyOdometer, parser.NextHexLong())
	pos.Set(model.KeySatellites, parser.NextHexInt())
	pos.Set(model.KeyCard, parser.NextNullable())
	pos.Set(model.KeyOdometer, parser.NextLong())
	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())
	return pos
}

func decodeRfid(m *ingest.Message, pos *model.Position, sentence string) *model.Position {
	parser := kit.NewParser(patternRfid, sentence)
	if !parser.Matches() {
		return nil
	}
	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))
	pos.Valid = true
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	return pos
}

func decodeObd(m *ingest.Message, pos *model.Position, sentence string) *model.Position {
	parser := kit.NewParser(patternObd, sentence)
	if !parser.Matches() {
		return nil
	}
	m.LastLocation(pos, nil)
	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyRpm, parser.NextInt())
	pos.Set(model.KeyObdSpeed, parser.NextInt())
	pos.Set(model.KeyThrottle, parser.NextDouble())
	pos.Set(model.KeyEngineLoad, parser.NextDouble())
	pos.Set(model.KeyCoolantTemp, parser.NextInt())
	pos.Set(model.KeyFuelConsumption, parser.NextDouble())
	pos.Set("averageFuelConsumption", parser.NextDouble())
	pos.Set("drivingRange", parser.NextDouble())
	pos.Set(model.KeyOdometer, parser.NextDouble())
	pos.Set("singleFuelConsumption", parser.NextDouble())
	pos.Set(model.KeyFuelUsed, parser.NextDouble())
	pos.Set(model.KeyDtcs, parser.NextInt())
	pos.Set("hardAccelerationCount", parser.NextInt())
	pos.Set("hardBrakingCount", parser.NextInt())
	return pos
}

func decodeObdA(m *ingest.Message, pos *model.Position, sentence string) *model.Position {
	parser := kit.NewParser(patternObdA, sentence)
	if !parser.Matches() {
		return nil
	}
	m.LastLocation(pos, nil)
	pos.Set("totalIgnitionNo", parser.NextIntDefault(0))
	pos.Set("totalDrivingTime", parser.NextDoubleDefault(0))
	pos.Set("totalIdlingTime", parser.NextDoubleDefault(0))
	pos.Set("averageHotStartTime", parser.NextIntDefault(0))
	pos.Set("averageSpeed", parser.NextIntDefault(0))
	pos.Set("historyHighestSpeed", parser.NextIntDefault(0))
	pos.Set("historyHighestRpm", parser.NextIntDefault(0))
	pos.Set("totalHarshAccerleration", parser.NextIntDefault(0))
	pos.Set("totalHarshBrake", parser.NextIntDefault(0))
	return pos
}

func decodeDtc(m *ingest.Message, pos *model.Position, sentence string) *model.Position {
	m.LastLocation(pos, nil)
	pos.Set(model.KeyDtcs, strings.ReplaceAll(sentence, ",", " "))
	return pos
}

func decodeRetransmission(m *ingest.Message, buf *kit.Buf, ds *ingest.DeviceSession) []*model.Position {
	positions := []*model.Position{}
	count := buf.ReadUnsignedByte()
	for i := 0; i < count; i++ {
		buf.ReadUnsignedByte() // alarm
		end := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '\\')
		if end < 0 {
			end = buf.WriterIndex() - 4
		}
		sentence := buf.ReadString(end - buf.ReaderIndex())

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		if pos = decodeRegular(pos, sentence); pos != nil {
			positions = append(positions, pos)
		}

		if buf.ReadableBytes() > 4 {
			buf.ReadUnsignedByte() // delimiter
		}
	}
	return positions
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2)     // header
	buf.ReadShort() // length
	id := buf.ReadSlice(7)
	command := buf.ReadUnsignedShort()

	switch command {
	case msgLogin:
		sendResponse(m, id, msgLoginResponse, []byte{0x01})
		return nil
	case msgHeartbeat:
		sendResponse(m, id, msgHeartbeat, []byte{0x01})
		return nil
	case msgServer:
		sendResponse(m, id, msgServer, kit.NettyBytes(m.Server(':')))
		return nil
	case msgUploadPhoto:
		imageIndex := buf.ReadSignedByte()
		d.media = m.NewMediaBuffer()
		sendResponse(m, id, msgUploadPhotoResponse, []byte{byte(imageIndex)})
		return nil
	case msgUploadComplete:
		imageIndex := buf.ReadSignedByte()
		sendResponse(m, id, msgRetransmission, []byte{byte(imageIndex), 0, 0})
		return nil
	}

	ds := identify(m, id)
	if ds == nil {
		return nil
	}

	switch command {
	case msgDataPhoto:
		buf.ReadSignedByte()    // image index
		buf.ReadUnsignedShort() // image footage
		buf.ReadUnsignedByte()  // total packets
		buf.ReadUnsignedByte()  // packet index
		if d.media == nil {
			kit.Throw("NullPointerException: no media buffer")
		}
		d.media.WriteBytes(buf.ReadBytes(buf.ReadableBytes() - 2 - 2))
		return nil
	case msgRetransmission:
		return decodeRetransmission(m, buf, ds)
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	switch command {
	case msgAlarm:
		alarmCode := buf.ReadUnsignedByte()
		pos.AddAlarm(decodeAlarm(m.DeviceModel(ds), alarmCode))
		if alarmCode >= 0x02 && alarmCode <= 0x05 {
			pos.Set(model.PrefixIn+strconv.Itoa(alarmCode), 1)
		} else if alarmCode >= 0x32 && alarmCode <= 0x35 {
			pos.Set(model.PrefixIn+strconv.Itoa(alarmCode-0x30), 0)
		}
	case msgPositionLogged:
		buf.Skip(6)
	case msgRfid:
		for i := 0; i < 15; i++ {
			rfid := buf.ReadUnsignedInt()
			if rfid != 0 {
				card := kit.Format("%010d", rfid)
				pos.Set("card"+strconv.Itoa(i+1), card)
				pos.Set(model.KeyDriverUniqueID, card)
			}
		}
	case msgPositionImage:
		buf.ReadSignedByte()   // image index
		buf.ReadUnsignedByte() // image upload type
		pos.Set(model.KeyImage, d.writeMedia(m, ds.UniqueID, "jpg"))
	}

	sentence := buf.GetLatin1(buf.ReaderIndex(), buf.ReadableBytes()-4)

	var result *model.Position
	switch command {
	case msgPosition, msgPositionLogged, msgAlarm, msgPositionImage:
		result = decodeRegular(pos, sentence)
	case msgRfid:
		result = decodeRfid(m, pos, sentence)
	case msgObdRt:
		result = decodeObd(m, pos, sentence)
	case msgObdRta:
		result = decodeObdA(m, pos, sentence)
	case msgDtc:
		result = decodeDtc(m, pos, sentence)
	}
	if result == nil {
		return nil
	}
	return result
}

// writeMedia is writeMediaFile(uniqueId, extension): the media buffer is stored and
// released.
func (d *Decoder) writeMedia(m *ingest.Message, uniqueID, extension string) string {
	var data []byte
	if d.media != nil {
		data = d.media.Bytes()
	}
	d.media = nil
	return m.WriteMedia(uniqueID, extension, data)
}

func encodeContent(m *ingest.Message, deviceID int64, typ int, content []byte) *kit.Buf {
	id := kit.ParseHex(kit.Substring(m.UniqueID(deviceID)+"FFFFFFFFFFFFFF", 0, 14))
	return frame(id, typ, content)
}

var outputModels = map[string]bool{"TK510": true, "GT08": true, "TK208": true, "TK228": true, "MT05": true}

func encodeOutputCommand(m *ingest.Message, deviceID int64, index, value int) *kit.Buf {
	device, err := m.Device(deviceID)
	if err != nil {
		panic(err)
	}
	if device == nil {
		kit.Throw("NullPointerException: device %d", deviceID)
	}
	var outputCount, outputType int
	if device.Model != nil && outputModels[*device.Model] {
		outputCount = 5
		outputType = msgOutputControl1
	} else {
		outputCount = 1
		alternative := m.LookupBool(deviceID, m.Protocol()+".alternative", false, true)
		if alternative != nil && *alternative {
			outputType = msgOutputControl1
		} else {
			outputType = msgOutputControl2
		}
	}
	content := make([]byte, 0, outputCount)
	for i := 1; i <= outputCount; i++ {
		if i == index {
			content = append(content, byte(value))
		} else {
			content = append(content, 2)
		}
	}
	return encodeContent(m, deviceID, outputType, content)
}

// rawOffset is TimeZone.getRawOffset: the zone's standard offset (without daylight
// saving), in seconds, as of now.
func rawOffset(loc *time.Location, now time.Time) int {
	t := now.In(loc)
	_, offset := t.Zone()
	if !t.IsDST() {
		return offset
	}
	for month := time.January; month <= time.December; month++ {
		u := time.Date(t.Year(), month, 1, 12, 0, 0, 0, loc)
		if !u.IsDST() {
			_, offset = u.Zone()
			return offset
		}
	}
	return offset
}

func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandPositionSingle:
		return encodeContent(m, c.DeviceID, msgTrackOnDemand, nil)
	case model.CommandPositionPeriodic:
		content := kit.Empty().WriteShort(kit.CommandInt(c, model.CommandKeyFrequency) / 10)
		return encodeContent(m, c.DeviceID, msgTrackByInterval, content.Bytes())
	case model.CommandOutputControl:
		index := kit.CommandInt(c, model.CommandKeyIndex) - 1
		value := kit.CommandInt(c, model.CommandKeyData)
		return encodeOutputCommand(m, c.DeviceID, index, value)
	case model.CommandEngineStop:
		return encodeOutputCommand(m, c.DeviceID, 1, 1)
	case model.CommandEngineResume:
		return encodeOutputCommand(m, c.DeviceID, 1, 0)
	case model.CommandAlarmGeofence:
		content := kit.Empty().WriteShort(kit.CommandInt(c, model.CommandKeyRadius))
		return encodeContent(m, c.DeviceID, msgMovementAlarm, content.Bytes())
	case model.CommandSetTimezone:
		zone := kit.CommandString(c, model.CommandKeyTimezone)
		if zone == nil {
			kit.Throw("NullPointerException: timezone")
		}
		offset := rawOffset(javatime.TimeZone(*zone), m.Now) / 60
		return encodeContent(m, c.DeviceID, msgTimeZone, []byte(strconv.Itoa(offset)))
	case model.CommandRequestPhoto:
		return encodeContent(m, c.DeviceID, msgTakePhoto, nil)
	case model.CommandRebootDevice:
		return encodeContent(m, c.DeviceID, msgRebootGps, nil)
	}
	return nil
}
