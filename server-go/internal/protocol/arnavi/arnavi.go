// Package arnavi is the Arnavi protocol (ArnaviProtocol): "$AV," text lines and binary
// tagged packages over the same TCP connection.
package arnavi

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "arnavi",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// decode is ArnaviProtocolDecoder: text lines start with '$', anything else is binary.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.GetByte(buf.ReaderIndex()) == '$' {
		return decodeText(m, msg)
	}
	return decodeBinary(m, msg)
}

const (
	headerLength        = 10
	packetWrapperLength = 8
	resultType          = 0xfd
	packageEndSign      = 0x5d
)

// framer is ArnaviFrameDecoder: the first binary frame is the 10-byte header, later ones
// are packages of records ending with 0x5d, or 4-byte results.
//
// Like the Java decoder, a package's type and records are read at absolute indexes.
type framer struct {
	started bool
}

// Framer returns a new ArnaviFrameDecoder.
func Framer() ingest.Framer {
	f := &framer{}
	return &ingest.BaseFramer{Decode: f.decode}
}

func (f *framer) decode(_ *ingest.Message, buf *kit.Buf) any {
	if buf.ReadableBytes() < 4 {
		return nil
	}
	if buf.GetByte(buf.ReaderIndex()) == '$' {
		index := buf.IndexOfIn("\r\n", buf.ReaderIndex(), buf.WriterIndex())
		if index > 0 {
			frame := buf.ReadSlice(index - buf.ReaderIndex())
			buf.Skip(2)
			return frame
		}
		return nil
	}
	var length int
	if !f.started {
		f.started = true
		length = headerLength
	} else if buf.GetUnsignedByte(1) == resultType {
		length = 4
	} else {
		index := 2
		for index+packetWrapperLength < buf.ReadableBytes() && buf.GetByte(index) != packageEndSign {
			index += packetWrapperLength + buf.GetUnsignedShortLE(index+1)
		}
		if buf.GetByte(index) != packageEndSign {
			return nil
		}
		length = index + 1
	}
	if buf.ReadableBytes() >= length {
		return buf.ReadSlice(length)
	}
	return nil
}

var pattern = kit.NewPattern().
	Text("$AV,").
	Number("Vd,").                 // type
	Number("(d+),").               // device id
	Number("(d+),").               // index
	Number("(d+),").               // power
	Number("(d+),").               // battery
	Number("-?d+,").
	Expression("[01],").           // movement
	Expression("([01]),").         // ignition
	Number("(d+),").               // input
	Number("d+,d+,").              // input 1
	Number("d+,d+,").Optional().   // input 2
	Expression("[01],").           // fix type
	Number("(d+),").               // satellites
	GroupBegin().
	Number("(d+.d+)?,").           // altitude
	Number("(?:d+.d+)?,").         // geoid height
	GroupEndWith("?").
	Number("(dd)(dd)(dd),").       // time (hhmmss)
	Number("(dd)(dd.d+)([NS]),").  // latitude
	Number("(ddd)(dd.d+)([EW]),"). // longitude
	Number("(d+.d+),").            // speed
	Number("(d+.d+),").            // course
	Number("(dd)(dd)(dd)").        // date (ddmmyy)
	Any().
	Compile()

// decodeText is ArnaviTextProtocolDecoder.
func decodeText(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	parser := kit.NewParser(pattern, kit.Latin1(buf.Bytes()))
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyIndex, parser.NextInt())
	pos.Set(model.KeyPower, float64(parser.MustInt())/100.0)
	pos.Set(model.KeyBattery, float64(parser.MustInt())/100.0)
	pos.Set(model.KeyIgnition, parser.MustInt() == 1)
	pos.Set(model.KeyInput, parser.NextInt())
	pos.Set(model.KeySatellites, parser.NextInt())

	pos.Altitude = parser.NextDoubleDefault(0)

	date := kit.NewDateBuilder(nil)
	hour, minute, second := parser.MustInt(), parser.MustInt(), parser.MustInt()
	date.SetTime(hour, minute, second)

	pos.Valid = true
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.MustDouble()
	pos.Course = parser.MustDouble()

	day, month, year := parser.MustInt(), parser.MustInt(), parser.MustInt()
	date.SetDateReverse(day, month, year)
	pos.SetTime(date.Date())
	return pos
}

const (
	headerStartSign   = 0xff
	headerVersion1    = 0x22
	headerVersion2    = 0x23
	headerVersion2Wifi = 0x25

	recordPing   = 0x00
	recordData   = 0x01
	recordText   = 0x03
	recordFile   = 0x04
	recordBinary = 0x06
)

func sendResponse(m *ingest.Message, version, index int) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteByte8(0x7b)
	if version == headerVersion1 {
		response.WriteByte8(0x00)
		response.WriteByte8(index)
	} else if version == headerVersion2 || version == headerVersion2Wifi {
		response.WriteByte8(0x04)
		response.WriteByte8(0x00)
		timeBytes := kit.Empty()
		timeBytes.WriteIntLE(int64(int32(m.Now.UnixMilli() / 1000)))
		response.WriteByte8(kit.Modulo256(timeBytes.Bytes()))
		response.WriteBuf(timeBytes)
	}
	response.WriteByte8(0x7d)
	m.Send(response)
}

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, length int, t time.Time) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(t)

	var cid, lac, mnc, mcc, rssi int

	endIndex := buf.ReaderIndex() + length
	for buf.ReaderIndex() < endIndex {
		tag := buf.ReadUnsignedByte()
		switch {
		case tag == 1:
			if battery := buf.ReadUnsignedShortLE(); battery != 0x0000 && battery != 0xffff {
				pos.Set(model.KeyBattery, float64(battery)/1000.0)
			}
			if power := buf.ReadUnsignedShortLE(); power != 0x0000 && power != 0xffff {
				pos.Set(model.KeyPower, float64(power)/1000.0)
			}
		case tag == 2:
			pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
		case tag == 3:
			pos.SetLatitude(float64(buf.ReadFloatLE()))
		case tag == 4:
			pos.SetLongitude(float64(buf.ReadFloatLE()))
		case tag == 5:
			pos.Course = float64(buf.ReadUnsignedByte() * 2)
			pos.Altitude = float64(buf.ReadUnsignedByte() * 10)
			satellites := buf.ReadUnsignedByte()
			pos.Valid = satellites != 0xfe && satellites != 0xff
			pos.Set(model.KeySatellites, kit.BetweenInt(satellites, 0, 4)+kit.BetweenInt(satellites, 4, 8))
			pos.Speed = float64(buf.ReadUnsignedByte())
		case tag == 6:
			switch buf.ReadUnsignedByte() {
			case 0x01:
				flags := buf.ReadUnsignedByte()
				pos.Set(model.KeyIgnition, kit.Check(int64(flags), 0))
				pos.Set("callButton", kit.Check(int64(flags), 1))
				pos.Set(model.KeyInput, buf.ReadUnsignedShortLE())
			case 0x06:
				key := "pulses" + strconv.Itoa(buf.ReadUnsignedByte())
				pos.Set(key, buf.ReadUnsignedShortLE())
			case 0x07:
				key := "freq" + strconv.Itoa(buf.ReadUnsignedByte())
				pos.Set(key, buf.ReadUnsignedShortLE())
			case 0x08:
				key := model.PrefixAdc + strconv.Itoa(buf.ReadUnsignedByte())
				pos.Set(key, float64(buf.ReadUnsignedShortLE())/1000.0)
			default:
				buf.Skip(3)
			}
		case tag == 7:
			cid = buf.ReadUnsignedShortLE()
			lac = buf.ReadUnsignedShortLE()
		case tag == 8:
			rssi = buf.ReadUnsignedByte()
			mcc = buf.ReadUnsignedShortLE()
			mnc = buf.ReadUnsignedByte()
		case tag == 9:
			pos.Set(model.KeyStatus, buf.ReadUnsignedIntLE())
		case tag >= 30 && tag <= 37:
			pos.Set(model.PrefixAdc+strconv.Itoa(tag-29), float64(buf.ReadUnsignedShortLE())/1000.0)
			buf.Skip(2)
		case tag >= 40 && tag <= 49:
			pos.Set(model.PrefixTemp+strconv.Itoa(tag-39), float64(buf.ReadShortLE())/100.0)
			buf.Skip(2)
		case tag == 51:
			pos.Set("canStatus", buf.ReadUnsignedIntLE())
		case tag == 52:
			pos.Set(model.KeyHours, buf.ReadUnsignedIntLE()*3600000/100)
		case tag == 53:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE()*10)
		case tag == 54:
			pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedIntLE())/10.0)
		case tag == 55:
			pos.Set(model.KeyFuelLevel, float64(buf.ReadUnsignedShortLE())/10.0)
			buf.Skip(2)
		case tag == 56:
			pos.Set(model.KeyFuel, buf.ReadUnsignedIntLE())
		case tag == 57:
			pos.Set(model.KeyRpm, buf.ReadUnsignedShortLE())
			buf.Skip(2)
		case tag == 58:
			pos.Set(model.KeyEngineTemp, buf.ReadUnsignedIntLE())
		case tag == 59:
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedIntLE())
		case tag >= 60 && tag <= 64:
			pos.Set("axleLoad"+strconv.Itoa(tag-59), buf.ReadUnsignedIntLE())
		case tag == 67:
			pos.Set("adBlueLevel", float64(buf.ReadUnsignedShortLE())/10.0)
			buf.Skip(2)
		case tag == 69:
			pos.Set(model.KeyEngineLoad, buf.ReadUnsignedByte())
			pos.Set(model.KeyThrottle, buf.ReadUnsignedByte())
			if motionStatus := buf.ReadUnsignedShortLE(); motionStatus != 0 {
				pos.Set("motionStatus", motionStatus)
			}
		case tag >= 71 && tag <= 79:
			if level := buf.ReadUnsignedShortLE(); level != 0x0000 && level != 0xffff {
				pos.Set("llsLevel"+strconv.Itoa(tag-70), level)
				pos.Set("llsTemp"+strconv.Itoa(tag-70), (float64(buf.ReadUnsignedShortLE())-100.0)/10.0)
			}
		case tag == 100:
			pos.Set("tachographOdometer", buf.ReadUnsignedIntLE()*10)
		case tag == 102:
			pos.Set("tachographPower", float64(buf.ReadUnsignedIntLE())/1000.0)
		case tag == 151:
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShortLE())/100.0)
			buf.Skip(2)
		default:
			buf.Skip(4)
		}
	}

	if mcc != 0 || mnc != 0 || lac != 0 || cid != 0 || rssi != 0 {
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
	}
	return pos
}

// decodeBinary is ArnaviBinaryProtocolDecoder.
func decodeBinary(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.ReadUnsignedByte() == headerStartSign {
		version := buf.ReadUnsignedByte()
		imei := strconv.FormatInt(buf.ReadLongLE(), 10)
		if m.Session(imei) != nil {
			sendResponse(m, version, 0)
		}
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	var positions []*model.Position
	result := func() any {
		if len(positions) == 0 {
			return nil
		}
		return positions
	}

	index := buf.ReadUnsignedByte()
	for buf.ReadableBytes() > 1 {
		switch typ := buf.ReadUnsignedByte(); typ {
		case recordPing, recordData, recordText, recordFile, recordBinary:
			length := buf.ReadUnsignedShortLE()
			t := time.UnixMilli(buf.ReadUnsignedIntLE() * 1000)
			if typ == recordData {
				positions = append(positions, decodePosition(m, ds, buf, length, t))
			} else {
				buf.Skip(length)
			}
			buf.ReadUnsignedByte()
		default:
			return result()
		}
	}

	sendResponse(m, headerVersion1, index)
	return result()
}
