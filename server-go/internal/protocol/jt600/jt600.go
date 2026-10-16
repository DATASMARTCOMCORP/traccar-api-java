// Package jt600 is the JT600 protocol (Jt600Protocol): binary '$' location reports and
// text '(...)' messages over TCP.
package jt600

import (
	"strconv"
	"strings"
	"time"

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
		Name:         "jt600",
		DataCommands: protocol.DataCommands("jt600"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Framer is Jt600FrameDecoder: binary frames carry their length, text frames end with
// ')' and are unescaped (0x3d escapes).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		switch buf.GetByte(buf.ReaderIndex()) {
		case '$':
			offset := 7
			if IsLongFormat(buf) {
				offset = 8
			}
			length := buf.GetUnsignedShort(buf.ReaderIndex()+offset) + 10
			if length <= buf.ReadableBytes() {
				return buf.ReadSlice(length)
			}
		case '(':
			endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ')')
			if endIndex >= 0 {
				result := kit.Empty()
				for buf.ReaderIndex() <= endIndex {
					b := buf.ReadUnsignedByte()
					if b == 0x3d {
						switch buf.ReadUnsignedByte() {
						case 0x15:
							result.WriteByte8(0x28)
						case 0x14:
							result.WriteByte8(0x29)
						case 0x11:
							result.WriteByte8(0x2c)
						case 0x00:
							result.WriteByte8(0x3d)
						}
					} else {
						result.WriteByte8(b)
					}
				}
				return result
			}
		default:
			kit.Throw("ParseException: unknown message")
		}
		return nil
	}}
}

// encode is Jt600ProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return "(S07,0)"
	case model.CommandEngineResume:
		return "(S07,1)"
	case model.CommandSetTimezone:
		id := kit.CommandString(c, model.CommandKeyTimezone)
		if id == nil {
			kit.Throw("NullPointerException")
		}
		return "(S09," + "1," + strconv.Itoa(rawOffset(javatime.TimeZone(*id))/60) + ")"
	case model.CommandRebootDevice:
		return "(S17)"
	}
	return nil
}

// rawOffset is TimeZone.getRawOffset in seconds: the standard (non-DST) offset.
func rawOffset(loc *time.Location) int {
	year := time.Now().Year()
	_, jan := time.Date(year, 1, 1, 0, 0, 0, 0, loc).Zone()
	_, jul := time.Date(year, 7, 1, 0, 0, 0, 0, loc).Zone()
	return min(jan, jul)
}

func convertCoordinate(raw int) float64 {
	degrees := raw / 1000000
	minutes := float64(raw%1000000) / 10000.0
	return float64(degrees) + minutes/60
}

func decodeStatus(pos *model.Position, buf *kit.Buf) {
	value := int64(buf.ReadUnsignedByte())
	pos.Set(model.KeyIgnition, kit.Check(value, 0))
	pos.Set(model.KeyDoor, kit.Check(value, 6))

	value = int64(buf.ReadUnsignedByte())
	pos.Set(model.KeyCharge, kit.Check(value, 0))
	pos.Set(model.KeyBlocked, kit.Check(value, 1))
	if kit.Check(value, 2) {
		pos.AddAlarm(model.AlarmSos)
	}
	if kit.Check(value, 3) || kit.Check(value, 4) {
		pos.AddAlarm(model.AlarmGpsAntennaCut)
	}
	if kit.Check(value, 4) {
		pos.AddAlarm(model.AlarmOverspeed)
	}

	value = int64(buf.ReadUnsignedByte())
	if kit.Check(value, 2) {
		pos.AddAlarm(model.AlarmFatigueDriving)
	}
	if kit.Check(value, 3) {
		pos.AddAlarm(model.AlarmTow)
	}
	buf.ReadUnsignedByte() // reserved
}

// IsLongFormat is Jt600ProtocolDecoder.isLongFormat.
func IsLongFormat(buf *kit.Buf) bool {
	return buf.GetUnsignedByte(buf.ReaderIndex()+8) == 0
}

// DecodeBinaryLocation is Jt600ProtocolDecoder.decodeBinaryLocation (also used by jt808).
func DecodeBinaryLocation(buf *kit.Buf, pos *model.Position) {
	b := kit.NewDateBuilder(nil).
		SetDay(kit.ReadBCD(buf, 2)).
		SetMonth(kit.ReadBCD(buf, 2)).
		SetYear(kit.ReadBCD(buf, 2)).
		SetHour(kit.ReadBCD(buf, 2)).
		SetMinute(kit.ReadBCD(buf, 2)).
		SetSecond(kit.ReadBCD(buf, 2))
	pos.SetTime(b.Date())

	latitude := convertCoordinate(kit.ReadBCD(buf, 8))
	longitude := convertCoordinate(kit.ReadBCD(buf, 9))

	flags := int64(buf.ReadSignedByte())
	pos.Valid = kit.Check(flags, 0)
	if kit.Check(flags, 1) {
		pos.SetLatitude(latitude)
	} else {
		pos.SetLatitude(-latitude)
	}
	if kit.Check(flags, 2) {
		pos.SetLongitude(longitude)
	} else {
		pos.SetLongitude(-longitude)
	}

	pos.Speed = float64(kit.ReadBCD(buf, 2))
	pos.Course = float64(buf.ReadUnsignedByte()) * 2.0
}

// CellFromCidLac is CellTower.fromCidLac(config, cid, lac): the configured
// geolocation.mcc and geolocation.mnc (0 when unset).
func CellFromCidLac(m *ingest.Message, cid int64, lac int) model.CellTower {
	mcc, err := m.Config().Int("geolocation.mcc", 0)
	if err != nil {
		panic(err)
	}
	mnc, err := m.Config().Int("geolocation.mnc", 0)
	if err != nil {
		panic(err)
	}
	return model.NewCellTower(mcc, mnc, lac, cid)
}

func alarmIf(pos *model.Position, cond bool, alarm string) {
	if cond {
		pos.AddAlarm(alarm)
	}
}

func decodeBinary(m *ingest.Message, buf *kit.Buf) any {
	positions := []*model.Position{}

	longFormat := IsLongFormat(buf)
	buf.ReadSignedByte() // header

	id := kit.StripLeading('0', buf.ReadSlice(5).HexDump())
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	protocolVersion := 0
	if longFormat {
		protocolVersion = buf.ReadUnsignedByte()
	}
	version := kit.FromInt(buf.ReadUnsignedByte(), 4)
	buf.ReadUnsignedShort() // length

	responseRequired := false

	for buf.ReadableBytes() >= 17 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		DecodeBinaryLocation(buf, pos)

		if longFormat {
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())

			buf.ReadUnsignedInt() // vehicle id combined

			status := buf.ReadUnsignedShort()
			s := int64(status)
			alarmIf(pos, kit.Check(s, 1), model.AlarmGeofenceEnter)
			alarmIf(pos, kit.Check(s, 2), model.AlarmGeofenceExit)
			alarmIf(pos, kit.Check(s, 3), model.AlarmPowerCut)
			alarmIf(pos, kit.Check(s, 4), model.AlarmVibration)
			if kit.Check(s, 5) {
				responseRequired = true
			}
			pos.Set(model.KeyBlocked, kit.Check(s, 7))
			alarmIf(pos, kit.Check(s, 8+3), model.AlarmLowBattery)
			alarmIf(pos, kit.Check(s, 8+6), model.AlarmFault)
			pos.Set(model.KeyStatus, status)

			battery := buf.ReadUnsignedByte()
			if battery == 0xff {
				pos.Set(model.KeyCharge, true)
			} else {
				pos.Set(model.KeyBatteryLevel, battery)
			}

			cid := buf.ReadUnsignedShort()
			lac := buf.ReadUnsignedShort()
			cell := CellFromCidLac(m, int64(cid), lac)
			cell.SetSignalStrength(buf.ReadUnsignedByte())
			pos.Network = model.NetworkOf(cell)

			if protocolVersion == 0x17 || protocolVersion == 0x19 {
				buf.ReadUnsignedByte() // geofence id
				buf.Skip(3)            // reserved
				buf.Skip(buf.ReadableBytes() - 1)
			}
		} else if version == 1 {
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			pos.Set(model.KeyPower, buf.ReadUnsignedByte())

			buf.ReadSignedByte() // other flags and sensors

			pos.Altitude = float64(buf.ReadUnsignedShort())

			cid := buf.ReadUnsignedShort()
			lac := buf.ReadUnsignedShort()
			rssi := buf.ReadUnsignedByte()

			if cid != 0 && lac != 0 {
				cell := CellFromCidLac(m, int64(cid), lac)
				cell.SetSignalStrength(rssi)
				pos.Network = model.NetworkOf(cell)
			} else {
				pos.Set(model.KeyRssi, rssi)
			}
		} else if version == 2 {
			fuel := buf.ReadUnsignedByte() << 8

			decodeStatus(pos, buf)
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)

			fuel += buf.ReadUnsignedByte()
			pos.Set(model.KeyFuel, fuel)
		} else if version == 3 {
			bits := kit.NewBitBuffer(buf)

			pos.Set("fuel1", bits.ReadUnsigned(12))
			pos.Set("fuel2", bits.ReadUnsigned(12))
			pos.Set("fuel3", bits.ReadUnsigned(12))
			pos.Set(model.KeyOdometer, int(int32(bits.ReadUnsigned(20)*1000)))

			status := bits.ReadUnsigned(24)
			pos.Set(model.KeyIgnition, kit.Check(int64(status), 0))
			pos.Set(model.KeyStatus, status)
		}

		positions = append(positions, pos)
	}

	index := buf.ReadUnsignedByte()

	if responseRequired {
		if protocolVersion < 0x19 {
			m.Send("(P35)")
		} else {
			m.Send("(P69,0," + strconv.Itoa(index) + ")")
		}
	}

	return positions
}

var patternW01 = kit.NewPattern().
	Text("(").
	Number("(d+),").           // id
	Text("W01,").              // type
	Number("(ddd)(dd.dddd),"). // longitude
	Expression("([EW]),").
	Number("(dd)(dd.dddd),"). // latitude
	Expression("([NS]),").
	Expression("([AV]),").   // validity
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(d+),").         // speed
	Number("(d+),").         // course
	Number("(d+),").         // power
	Number("(d+),").         // gps signal
	Number("(d+),").         // gsm signal
	Number("(d+),").         // alert type
	Any().
	Compile()

func decodeW01(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternW01, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Valid = parser.Next() == "A"

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyPower, parser.NextDoubleDefault(0))
	pos.Set(model.KeyGps, parser.NextIntDefault(0))
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))
	pos.Set("alertType", parser.NextIntDefault(0))
	return pos
}

var patternU01 = kit.NewPattern().
	Text("(").
	Number("(d+),").            // id
	Number("(Udd),").           // type
	Number("d+,").Optional().   // alarm
	Number("(dd)(dd)(dd),").    // date (ddmmyy)
	Number("(dd)(dd)(dd),").    // time (hhmmss)
	Expression("([TF]),").      // validity
	Number("(d+.d+),([NS]),").  // latitude
	Number("(d+.d+),([EW]),").  // longitude
	Number("(d+.?d*),").        // speed
	Number("(d+),").            // course
	Number("(d+),").            // satellites
	Number("(d+)%,").           // battery
	Expression("([01]+),").     // status
	Number("(d+),").            // cid
	Number("(d+),").            // lac
	Number("(d+),").            // gsm signal
	Number("(d+),").            // odometer
	Number("(d+)").             // serial number
	Number(",(xx)").Optional(). // checksum
	Any().
	Compile()

func decodeU01(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternU01, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	typ := parser.Next()

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Valid = parser.Next() == "T"
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))

	pos.Speed = units.KnotsFromMph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Set(model.KeyBatteryLevel, parser.NextIntDefault(0))
	pos.Set(model.KeyStatus, parser.NextBinIntDefault(0))

	cid := parser.NextIntDefault(0)
	lac := parser.NextIntDefault(0)
	cell := CellFromCidLac(m, int64(cid), lac)
	cell.SetSignalStrength(parser.NextIntDefault(0))
	pos.Network = model.NetworkOf(cell)

	pos.Set(model.KeyOdometer, parser.NextLongDefault(0)*1000)
	pos.Set(model.KeyIndex, parser.NextIntDefault(0))

	switch typ {
	case "U01", "U02", "U03":
		m.Send("(S39)")
	case "U06":
		m.Send("(S20)")
	}
	return pos
}

var patternP45 = kit.NewPattern().
	Text("(").
	Number("(d+),").           // id
	Text("P45,").              // type
	Number("(dd)(dd)(dd),").   // date (ddmmyy)
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Number("(d+.d+),([NS]),"). // latitude
	Number("(d+.d+),([EW]),"). // longitude
	Expression("([AV]),").     // validity
	Number("(d+),").           // speed
	Number("(d+),").           // course
	Number("(d+),").           // event source
	Number("d+,").             // unlock verification
	Number("(d+),").           // rfid
	Number("d+,").             // password verification
	Number("d+,").             // incorrect password count
	Number("(d+)").            // index
	Any().
	Compile()

func decodeP45(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternP45, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Valid = parser.Next() == "A"

	pos.Speed = units.KnotsFromMph(parser.MustDouble())
	pos.Course = parser.MustDouble()

	pos.Set("eventSource", parser.NextInt())

	rfid := parser.Next()
	if rfid != "0000000000" {
		pos.Set(model.KeyDriverUniqueID, rfid)
	}

	index := parser.MustInt()
	m.Send("(P69,0," + strconv.Itoa(index) + ")")
	return pos
}

func decodePeripherals(m *ingest.Message, buf *kit.Buf) any {
	ds := m.Session(buf.GetLatin1(1, 10))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	startIndex := 0
	for i := 0; i < 6; i++ {
		startIndex = buf.IndexOf(startIndex+1, buf.WriterIndex(), ',')
	}
	buf.SetReaderIndex(startIndex + 1)

	DecodeBinaryLocation(buf, pos)

	buf.Skip(6)            // sensor time
	buf.Skip(5)            // sensor id
	buf.ReadUnsignedByte() // sensor index
	pos.Set("sensorBattery", float64(buf.ReadUnsignedShort())/100.0)
	pos.Set("sensorBatteryLevel", buf.ReadUnsignedByte())
	pos.Set("sensorRssi", -buf.ReadUnsignedByte())

	if buf.ReadUnsignedByte() == 1 {
		temperature := buf.ReadUnsignedShort()
		if temperature != 0xffff {
			value := temperature & 0xfff
			if temperature&0xf000 > 0 {
				value = -value
			}
			pos.Set(model.PrefixTemp+"1", float64(value)/10.0)
		}
		pos.Set(model.KeyHumidity, buf.ReadUnsignedByte())
		buf.ReadUnsignedShort() // data count
		buf.ReadUnsignedByte()  // status
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	switch buf.GetByte(0) {
	case '$':
		return decodeBinary(m, buf)
	case '(':
		if buf.IndexOfIn("WLNET,5,", buf.ReaderIndex(), buf.WriterIndex()) > 0 {
			return decodePeripherals(m, buf)
		}
		sentence := kit.Latin1(buf.Bytes())
		switch {
		case strings.Contains(sentence, "W01"):
			return decodeW01(m, sentence)
		case strings.Contains(sentence, "P45"):
			return decodeP45(m, sentence)
		default:
			return decodeU01(m, sentence)
		}
	}
	return nil
}
