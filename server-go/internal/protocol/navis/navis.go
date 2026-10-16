// Package navis is the Navis protocol (NavisProtocol): NTCB binary messages ("@NTC"
// header) and the FLEX formats they negotiate ("~A", "~T", "~C", "~E", "~X").
package navis

import (
	"math"
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
		Name:         "navis",
		DataCommands: protocol.DataCommands("navis"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			return ingest.Pipeline{
				Framer:  &Framer{Decoder: d},
				Decoder: d,
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

const (
	ntcbHeaderLength = 16
	ntcbLengthOffset = 12
	flexHeaderLength = 2
)

// Framer is NavisFrameDecoder. Until FlexDataSize is set, it takes it from the
// connection's decoder (once a FLEX negotiation has set it there).
type Framer struct {
	FlexDataSize int
	Decoder      *Decoder
}

// Frame splits one message.
func (f *Framer) Frame(m *ingest.Message, in *kit.Buf) any {
	return (&ingest.BaseFramer{Decode: f.decode}).Frame(m, in)
}

func (f *Framer) decode(_ *ingest.Message, buf *kit.Buf) any {
	if buf.GetByte(buf.ReaderIndex()) == 0x7F {
		return buf.ReadSlice(1) // keep alive
	}

	if f.Decoder != nil && f.FlexDataSize == 0 {
		f.FlexDataSize = f.Decoder.flexDataSize
	}

	if f.FlexDataSize > 0 {
		if buf.ReadableBytes() > flexHeaderLength {
			length := 0
			switch buf.GetString(buf.ReaderIndex(), 2) {
			// FLEX 1.0
			case "~A":
				length = f.FlexDataSize*int(buf.GetByte(buf.ReaderIndex()+flexHeaderLength)) + 1 + 1
			case "~T":
				length = f.FlexDataSize + 4 + 1
			case "~C":
				length = f.FlexDataSize + 1
			// FLEX 2.0 (Extra packages)
			case "~E":
				length++
				for i := 0; i < int(buf.GetByte(buf.ReaderIndex()+flexHeaderLength)); i++ {
					if buf.ReadableBytes() > flexHeaderLength+length+1 {
						// An absolute index, as the Java code reads it.
						length += buf.GetUnsignedShort(length+flexHeaderLength) + 2
					} else {
						return nil
					}
				}
				length++
			case "~X":
				length = buf.GetUnsignedShortLE(buf.ReaderIndex()+flexHeaderLength) + 4 + 1
			default:
				return nil
			}
			if buf.ReadableBytes() >= flexHeaderLength+length {
				return buf.ReadSlice(buf.ReadableBytes())
			}
		}
	} else {
		if buf.ReadableBytes() < ntcbHeaderLength {
			return nil
		}
		length := ntcbHeaderLength + buf.GetUnsignedShortLE(buf.ReaderIndex()+ntcbLengthOffset)
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
	}
	return nil
}

var flexFieldsSizes = []int{
	4, 2, 4, 1, 1, 1, 1, 1, 4, 4, 4, 4, 4, 2, 4, 4, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 4, 4, 2, 2,
	4, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 2, 4, 2, 1, 4, 2, 2, 2, 2, 2, 1, 1, 1, 2, 4, 2, 1,
	/* FLEX 2.0 */
	8, 2, 1, 16, 4, 2, 4, 37, 1, 1, 1, 1, 1, 1, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 6, 12, 24, 48, 1, 1, 1, 1, 4, 4,
	1, 4, 2, 6, 2, 6, 2, 2, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 1,
	/* FLEX 3.0 */
	1, 1, 1, 1, 4, 4, 4, 4, 4, 4, 2, 2, 2, 2, 2, 2, 1, 1, 2, 3, 2, 1, 1, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 2, 4, 4, 4, 2, 4, 2, 2, 4, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 4, 4,
	4, 4, 6, 3, 1, 2, 2, 1, 4, 5, 4, 4, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 8, 8, 8,
}

// NTCB position formats.
const (
	f10 = 0x01
	f20 = 0x02
	f30 = 0x03
	f40 = 0x04
	f50 = 0x05
	f51 = 0x15
	f52 = 0x25
	f60 = 0x06
)

// Decoder is NavisProtocolDecoder: it keeps the NTCB header of the last message for its
// replies and the negotiated FLEX fields.
type Decoder struct {
	prefix           string
	deviceUniqueID   int64
	serverID         int64
	flexDataSize     int
	flexBitFieldSize int
	flexBitField     [32]byte
}

func isFormat(t int, types ...int) bool {
	for _, i := range types {
		if t == i {
			return true
		}
	}
	return false
}

func check(v, bit int) bool { return kit.Check(int64(v), bit) }

func parseNtcbPosition(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	var format int
	if buf.GetUnsignedByte(buf.ReaderIndex()) == 0 {
		format = buf.ReadUnsignedShortLE()
	} else {
		format = buf.ReadUnsignedByte()
	}
	pos.Set("format", format)

	pos.Set(model.KeyIndex, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())

	buf.Skip(6) // event time

	armedStatus := buf.ReadUnsignedByte()
	if isFormat(format, f10, f20, f30, f40, f50, f51, f52) {
		pos.Set(model.KeyArmed, kit.ToInt(armedStatus, 7))
		if check(armedStatus, 7) {
			pos.AddAlarm(model.AlarmGeneral)
		}
	} else if isFormat(format, f60) {
		pos.Set(model.KeyArmed, check(armedStatus, 0))
		if check(armedStatus, 1) {
			pos.AddAlarm(model.AlarmGeneral)
		}
	}

	pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())

	if isFormat(format, f10, f20, f30) {
		output := buf.ReadUnsignedShortLE()
		pos.Set(model.KeyOutput, output)
		for i := range 16 {
			pos.Set(model.PrefixOut+itoa(i+1), check(output, i))
		}
	} else if isFormat(format, f50, f51, f52) {
		extField := buf.ReadUnsignedByte()
		pos.Set(model.KeyOutput, kit.ToInt(extField, 2))
		pos.Set(model.PrefixOut+"1", check(extField, 0))
		pos.Set(model.PrefixOut+"2", check(extField, 1))
		pos.Set(model.KeySatellites, kit.FromInt(extField, 2))
	} else if isFormat(format, f40, f60) {
		output := buf.ReadUnsignedByte()
		pos.Set(model.KeyOutput, kit.ToInt(output, 4))
		for i := range 4 {
			pos.Set(model.PrefixOut+itoa(i+1), check(output, i))
		}
	}

	if isFormat(format, f10, f20, f30, f40) {
		input := buf.ReadUnsignedShortLE()
		pos.Set(model.KeyInput, input)
		if !isFormat(format, f40) {
			for i := range 16 {
				pos.Set(model.PrefixIn+itoa(i+1), check(input, i))
			}
		} else {
			pos.Set(model.PrefixIn+"1", check(input, 0))
			pos.Set(model.PrefixIn+"2", check(input, 1))
			pos.Set(model.PrefixIn+"3", check(input, 2))
			pos.Set(model.PrefixIn+"4", check(input, 3))
			pos.Set(model.PrefixIn+"5", kit.BetweenInt(input, 4, 7))
			pos.Set(model.PrefixIn+"6", kit.BetweenInt(input, 7, 10))
			pos.Set(model.PrefixIn+"7", kit.BetweenInt(input, 10, 12))
			pos.Set(model.PrefixIn+"8", kit.BetweenInt(input, 12, 14))
		}
	} else if isFormat(format, f50, f51, f52, f60) {
		input := buf.ReadUnsignedByte()
		pos.Set(model.KeyInput, input)
		for i := range 8 {
			pos.Set(model.PrefixIn+itoa(i+1), check(input, i))
		}
	}

	pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)

	if isFormat(format, f10, f20, f30) {
		pos.Set(model.PrefixTemp+"1", buf.ReadShortLE())
	}

	if isFormat(format, f10, f20, f50, f51, f52, f60) {
		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShortLE())
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShortLE())
	}
	if isFormat(format, f60) {
		pos.Set(model.PrefixAdc+"3", buf.ReadUnsignedShortLE())
	}

	// Impulse counters
	if isFormat(format, f20, f50, f51, f52, f60) {
		buf.ReadUnsignedIntLE()
		buf.ReadUnsignedIntLE()
	}

	if isFormat(format, f60) {
		// Fuel
		buf.ReadUnsignedShortLE()
		buf.ReadUnsignedShortLE()
		buf.ReadSignedByte()
		buf.ReadShortLE()
		for range 7 {
			buf.ReadSignedByte()
			buf.ReadUnsignedShortLE()
		}

		pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))
		pos.Set(model.PrefixTemp+"2", int(buf.ReadSignedByte()))
		pos.Set(model.PrefixTemp+"3", int(buf.ReadSignedByte()))
		pos.Set(model.PrefixTemp+"4", int(buf.ReadSignedByte()))
		pos.Set(model.KeyAxleWeight, int(buf.ReadIntLE()))
		pos.Set(model.KeyRpm, buf.ReadUnsignedShortLE())
	}

	if isFormat(format, f20, f50, f51, f52, f60) {
		navSensorState := buf.ReadUnsignedByte()
		pos.Valid = check(navSensorState, 1)
		if isFormat(format, f60) {
			pos.Set(model.KeySatellites, kit.FromInt(navSensorState, 2))
		}

		hour, minute, second := buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()
		day, month, year := buf.ReadUnsignedByte(), buf.ReadUnsignedByte()+1, buf.ReadUnsignedByte()
		pos.SetTime(kit.NewDateBuilder(nil).SetTime(hour, minute, second).SetDateReverse(day, month, year).Date())

		if isFormat(format, f60) {
			pos.SetLatitude(float64(buf.ReadIntLE()) / 600000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 600000.0)
			pos.Altitude = float64(buf.ReadIntLE()) / 10.0
		} else {
			pos.SetLatitude(float64(buf.ReadFloatLE()) / math.Pi * 180)
			pos.SetLongitude(float64(buf.ReadFloatLE()) / math.Pi * 180)
		}

		pos.Speed = units.KnotsFromKph(float64(buf.ReadFloatLE()))
		pos.Course = float64(buf.ReadUnsignedShortLE())

		pos.Set(model.KeyOdometer, float64(buf.ReadFloatLE()*1000))
		pos.Set(model.KeyDistance, float64(buf.ReadFloatLE()*1000))

		// Segment times
		buf.ReadUnsignedShortLE()
		buf.ReadUnsignedShortLE()
	}

	// Other
	if isFormat(format, f51, f52) {
		buf.ReadUnsignedShortLE()
		buf.ReadSignedByte()
		buf.ReadUnsignedShortLE()
		buf.ReadUnsignedShortLE()
		buf.ReadSignedByte()
		buf.ReadUnsignedShortLE()
		buf.ReadUnsignedShortLE()
		buf.ReadSignedByte()
		buf.ReadUnsignedShortLE()
	}

	// Four temperature sensors
	if isFormat(format, f40, f52) {
		pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))
		pos.Set(model.PrefixTemp+"2", int(buf.ReadSignedByte()))
		pos.Set(model.PrefixTemp+"3", int(buf.ReadSignedByte()))
		pos.Set(model.PrefixTemp+"4", int(buf.ReadSignedByte()))
	}

	return pos
}

func itoa(i int) string { return kit.Format("%d", i) }

// index is position.getLong(KEY_INDEX) cast to int.
func index(pos *model.Position) int64 {
	v, ok := pos.Attributes.Get(model.KeyIndex)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return int64(int32(n))
	case int:
		return int64(int32(n))
	}
	return 0
}

func (d *Decoder) processNtcbSingle(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) any {
	pos := parseNtcbPosition(m, ds, buf)

	response := kit.Empty()
	response.WriteASCII("*<T")
	response.WriteIntLE(index(pos))
	d.sendNtcbReply(m, response)

	if !pos.FixTime.IsZero() {
		return pos
	}
	return nil
}

func (d *Decoder) processNtcbArray(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) any {
	positions := []*model.Position{}
	count := buf.ReadUnsignedByte()

	for range count {
		pos := parseNtcbPosition(m, ds, buf)
		if !pos.FixTime.IsZero() {
			positions = append(positions, pos)
		}
	}

	response := kit.Empty()
	response.WriteASCII("*<A")
	response.WriteByte8(count)
	d.sendNtcbReply(m, response)

	if len(positions) == 0 {
		return nil
	}
	return positions
}

func (d *Decoder) checkFlexBitfield(index int) bool {
	byteIndex := index / 8
	bitIndex := index % 8
	return kit.Check(int64(int8(d.flexBitField[byteIndex])), 7-bitIndex)
}

// nullable is Java's `condition ? value : null` for a boxed value.
func nullable[T any](ok bool, v T) *T {
	if ok {
		return &v
	}
	return nil
}

func (d *Decoder) parseFlexPosition(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	status := 0
	input := 0
	output := 0

	for i := 0; i < d.flexBitFieldSize; i++ {
		if !d.checkFlexBitfield(i) {
			continue
		}

		switch {
		case i == 0:
			pos.Set(model.KeyIndex, buf.ReadUnsignedIntLE())
		case i == 1:
			pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())
		case i == 2:
			pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		case i == 3:
			armedStatus := buf.ReadUnsignedByte()
			pos.Set(model.KeyArmed, check(armedStatus, 0))
			if check(armedStatus, 1) {
				pos.AddAlarm(model.AlarmGeneral)
			}
		case i == 4:
			status = buf.ReadUnsignedByte()
			pos.Set(model.KeyStatus, status)
		case i == 5:
			status2 := buf.ReadUnsignedByte()
			pos.Set(model.KeyStatus, int(int16(kit.ToInt(status, 8)|status2<<8)))
		case i == 6:
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		case i == 7:
			navSensorState := buf.ReadUnsignedByte()
			pos.Valid = check(navSensorState, 1)
			pos.Set(model.KeySatellites, kit.FromInt(navSensorState, 2))
		case i == 8:
			pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		case i == 9:
			pos.SetLatitude(float64(buf.ReadIntLE()) / 600000.0)
		case i == 10:
			pos.SetLongitude(float64(buf.ReadIntLE()) / 600000.0)
		case i == 11:
			pos.Altitude = float64(buf.ReadIntLE()) / 10.0
		case i == 12:
			pos.Speed = units.KnotsFromKph(float64(buf.ReadFloatLE()))
		case i == 13:
			pos.Course = float64(buf.ReadUnsignedShortLE())
		case i == 14:
			pos.Set(model.KeyOdometer, float64(buf.ReadFloatLE()*1000))
		case i == 15:
			pos.Set(model.KeyDistance, float64(buf.ReadFloatLE()*1000))
		case i == 18:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)
		case i == 19:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)
		case i >= 20 && i <= 27:
			pos.Set(model.PrefixAdc+itoa(i-19), buf.ReadUnsignedShortLE())
		case i == 28:
			input = buf.ReadUnsignedByte()
			pos.Set(model.KeyInput, input)
			for k := range 8 {
				pos.Set(model.PrefixIn+itoa(k+1), check(input, k))
			}
		case i == 29:
			input2 := buf.ReadUnsignedByte()
			pos.Set(model.KeyInput, int(int16(kit.ToInt(input, 8)|input2<<8)))
			for k := range 8 {
				pos.Set(model.PrefixIn+itoa(k+9), check(input2, k))
			}
		case i == 30:
			output = buf.ReadUnsignedByte()
			pos.Set(model.KeyOutput, output)
			for k := range 8 {
				pos.Set(model.PrefixOut+itoa(k+1), check(output, k))
			}
		case i == 31:
			output2 := buf.ReadUnsignedByte()
			pos.Set(model.KeyOutput, int(int16(kit.ToInt(output, 8)|output2<<8)))
			for k := range 8 {
				pos.Set(model.PrefixOut+itoa(k+9), check(output2, k))
			}
		case i == 32 || i == 33:
			pos.Set(model.PrefixCount+itoa(i-31), buf.ReadUnsignedIntLE())
		case i == 34 || i == 35:
			pos.Set("freq"+itoa(i-33), buf.ReadUnsignedShortLE())
		case i == 36:
			pos.Set(model.KeyHours, buf.ReadUnsignedIntLE()*1000)
		case i >= 37 && i <= 42:
			value := buf.ReadUnsignedShortLE()
			pos.Set(model.KeyFuel+itoa(i-36), nullable(value < 65500, value))
		case i == 43:
			value := buf.ReadUnsignedShortLE()
			pos.Set(model.KeyFuel, nullable(value < 65500, value))
		case i >= 44 && i <= 51:
			pos.Set(model.PrefixTemp+itoa(i-43), int(buf.ReadSignedByte()))
		case i == 52:
			value := buf.ReadUnsignedShortLE()
			if value != 0x7FFF {
				if check(value, 15) {
					pos.Set("obdFuelLevel", kit.ToInt(value, 14))
				} else {
					pos.Set("obdFuel", float64(kit.ToInt(value, 14))/10.0)
				}
			}
		case i == 53:
			fuelUsed := float64(buf.ReadFloatLE()) * 0.5
			pos.Set(model.KeyFuelUsed, nullable(fuelUsed >= 0, fuelUsed))
		case i == 54:
			value := buf.ReadUnsignedShortLE()
			pos.Set(model.KeyRpm, nullable(value != 0xFFFF, value))
		case i == 55:
			value := int(buf.ReadSignedByte())
			pos.Set(model.KeyCoolantTemp, nullable(value != -128, value))
		case i == 56:
			pos.Set(model.KeyObdOdometer, float64(buf.ReadFloatLE()*1000))
		case i >= 57 && i <= 61:
			value := buf.ReadUnsignedShortLE()
			pos.Set("axleWeight"+itoa(i-56), nullable(value != 0xFFFF, value))
		case i == 62:
			value := buf.ReadUnsignedByte()
			pos.Set("acceleratorPosition", nullable(value != 0xFF, value))
		case i == 63:
			value := buf.ReadUnsignedByte()
			pos.Set("brakePosition", nullable(value != 0xFF, value))
		case i == 64:
			value := buf.ReadUnsignedByte()
			pos.Set(model.KeyEngineLoad, nullable(value != 0xFF, value))
		case i == 65:
			value := buf.ReadUnsignedShortLE()
			if value != 0x7FFF {
				if check(value, 15) {
					pos.Set("obdAdBlueLevel", kit.ToInt(value, 14))
				} else {
					pos.Set("obdAdBlue", float64(kit.ToInt(value, 14))/10.0)
				}
			}
		case i == 66:
			pos.Set("obdHours", buf.ReadUnsignedIntLE()*1000)
		case i == 67:
			value := buf.ReadUnsignedShortLE()
			pos.Set(model.KeyOdometerService, nullable(value != 0xFFFF, int(int32(value*5000))))
		case i == 68:
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())

		// FLEX 2.0
		case i == 69:
			satVisible := 0
			for range 8 {
				satVisible += buf.ReadUnsignedByte()
			}
			pos.Set(model.KeySatellitesVisible, satVisible)
		case i == 70:
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
			pos.Set(model.KeyPdop, float64(buf.ReadUnsignedByte())/10.0)
		case i >= 77 && i <= 82:
			pos.Set("fuelTemp"+itoa(i-76), int(buf.ReadSignedByte()))
		case i >= 162 && i <= 165:
			value := buf.ReadShortLE()
			pos.Set(model.PrefixTemp+itoa(i+9-162), nullable(value != -32768, float64(value)/20.0))
		case i >= 166 && i <= 169:
			value := buf.ReadUnsignedByte()
			pos.Set("humidity"+itoa(i-165), nullable(value != 0xFF, float64(value)*0.5))
		case i >= 206 && i <= 221:
			pos.Set("user1Byte"+itoa(i-205), buf.ReadUnsignedByte())
		case i >= 222 && i <= 236:
			pos.Set("user2Byte"+itoa(i-221), buf.ReadUnsignedShortLE())
		case i >= 237 && i <= 251:
			pos.Set("user4Byte"+itoa(i-236), buf.ReadUnsignedIntLE())
		case i >= 252 && i <= 254:
			pos.Set("user8Byte"+itoa(i-251), buf.ReadLongLE())
		case i < len(flexFieldsSizes):
			buf.Skip(flexFieldsSizes[i])
		}
	}

	return pos
}

func (d *Decoder) parseFlex20Position(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	length := buf.ReadUnsignedShort()
	if length <= buf.ReadableBytes() && buf.ReadUnsignedByte() == 0x0A {

		buf.ReadUnsignedByte() // length of static part

		pos.Set(model.KeyIndex, buf.ReadUnsignedIntLE())

		pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())
		buf.ReadUnsignedInt() // event time

		navSensorState := buf.ReadUnsignedByte()
		pos.Valid = check(navSensorState, 1)
		pos.Set(model.KeySatellites, kit.FromInt(navSensorState, 2))

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		pos.SetLatitude(float64(buf.ReadIntLE()) / 600000.0)
		pos.SetLongitude(float64(buf.ReadIntLE()) / 600000.0)
		pos.Altitude = float64(buf.ReadIntLE()) / 10.0
		pos.Speed = units.KnotsFromKph(float64(buf.ReadFloatLE()))
		pos.Course = float64(buf.ReadUnsignedShortLE())
		pos.Set(model.KeyOdometer, float64(buf.ReadFloatLE()*1000))

		buf.Skip(length - buf.ReaderIndex() - 1) // skip unused part
	}

	return pos
}

type flexParser func(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position

func (d *Decoder) processFlexSingle(m *ingest.Message, parser flexParser, flexHeader string, ds *ingest.DeviceSession, buf *kit.Buf) any {
	if flexHeader != "~C" {
		buf.ReadUnsignedInt() // event index
	}

	pos := parser(m, ds, buf)

	response := kit.Empty()
	response.WriteASCII(flexHeader)
	response.WriteIntLE(index(pos))
	sendFlexReply(m, response)

	if !pos.FixTime.IsZero() {
		return pos
	}
	return nil
}

func (d *Decoder) processFlexArray(m *ingest.Message, parser flexParser, flexHeader string, ds *ingest.DeviceSession, buf *kit.Buf) any {
	positions := []*model.Position{}
	count := buf.ReadUnsignedByte()

	for range count {
		pos := parser(m, ds, buf)
		if !pos.FixTime.IsZero() {
			positions = append(positions, pos)
		}
	}

	response := kit.Empty()
	response.WriteASCII(flexHeader)
	response.WriteByte8(count)
	sendFlexReply(m, response)

	if len(positions) == 0 {
		return nil
	}
	return positions
}

func (d *Decoder) processFlexNegotiation(m *ingest.Message, buf *kit.Buf) any {
	if buf.ReadUnsignedByte() != 0xB0 {
		return nil
	}

	flexProtocolVersion := buf.ReadUnsignedByte()
	flexStructVersion := buf.ReadUnsignedByte()

	d.flexBitFieldSize = buf.ReadUnsignedByte()
	if d.flexBitFieldSize > len(flexFieldsSizes) {
		return nil
	}

	copy(d.flexBitField[:], buf.ReadBytes((d.flexBitFieldSize+7)/8))

	d.flexDataSize = 0
	for i := 0; i < d.flexBitFieldSize; i++ {
		if d.checkFlexBitfield(i) {
			d.flexDataSize += flexFieldsSizes[i]
		}
	}

	response := kit.Empty()
	response.WriteASCII("*<FLEX")
	response.WriteByte8(0xB0)
	response.WriteByte8(flexProtocolVersion)
	response.WriteByte8(flexStructVersion)
	d.sendNtcbReply(m, response)

	return nil
}

func (d *Decoder) processHandshake(m *ingest.Message, buf *kit.Buf) any {
	buf.ReadSignedByte() // colon
	if m.Session(kit.Latin1(buf.Bytes())) != nil {
		d.sendNtcbReply(m, kit.NewBuf(kit.NettyBytes("*<S")))
	}
	return nil
}

func (d *Decoder) sendNtcbReply(m *ingest.Message, data *kit.Buf) {
	header := kit.Empty()
	header.WriteASCII(d.prefix)
	header.WriteIntLE(int64(int32(d.deviceUniqueID)))
	header.WriteIntLE(int64(int32(d.serverID)))
	header.WriteShortLE(data.ReadableBytes())
	header.WriteByte8(kit.XOR(data.Bytes()))
	header.WriteByte8(kit.XOR(header.Bytes()))
	m.Send(header.WriteBuf(data))
}

func encodeContent(receiver, sender int64, content []byte) *kit.Buf {
	buf := kit.Empty()
	buf.WriteASCII("@NTC")
	buf.WriteIntLE(receiver)
	buf.WriteIntLE(sender)
	buf.WriteShortLE(len(content))
	buf.WriteByte8(kit.XOR(content))
	buf.WriteByte8(kit.XOR(buf.Bytes()))
	buf.WriteBytes(content)
	return buf
}

func sendFlexReply(m *ingest.Message, data *kit.Buf) {
	data.WriteByte8(kit.CRC8(kit.CRC8_EGTS, data.Bytes()))
	m.Send(data)
}

func (d *Decoder) decodeNtcb(m *ingest.Message, buf *kit.Buf) any {
	d.prefix = buf.GetString(buf.ReaderIndex(), 4)
	buf.Skip(kit.Len(d.prefix)) // prefix @NTC by default
	d.serverID = buf.ReadUnsignedIntLE()
	d.deviceUniqueID = buf.ReadUnsignedIntLE()
	length := buf.ReadUnsignedShortLE()
	buf.Skip(2) // header and data XOR checksum

	if length == 0 {
		return nil // keep alive message
	}

	t := buf.GetString(buf.ReaderIndex(), 3)
	buf.Skip(kit.Len(t))

	if t == "*>S" {
		return d.processHandshake(m, buf)
	}
	ds := m.Session()
	if ds == nil {
		return nil
	}
	switch t {
	case "*>A":
		return d.processNtcbArray(m, ds, buf)
	case "*>T":
		return d.processNtcbSingle(m, ds, buf)
	case "*>F":
		buf.Skip(3)
		return d.processFlexNegotiation(m, buf)
	case "*@C":
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, "*@C"+buf.ReadString(length-3))
		return pos
	}
	return nil
}

func (d *Decoder) decodeFlex(m *ingest.Message, buf *kit.Buf) any {
	if buf.GetByte(buf.ReaderIndex()) == 0x7F {
		return nil // keep alive
	}

	t := buf.GetString(buf.ReaderIndex(), 2)
	buf.Skip(kit.Len(t))

	ds := m.Session()
	if ds == nil {
		return nil
	}
	switch t {
	// FLEX 1.0
	case "~A":
		return d.processFlexArray(m, d.parseFlexPosition, t, ds, buf)
	case "~T", "~C":
		return d.processFlexSingle(m, d.parseFlexPosition, t, ds, buf)
	// FLEX 2.0 (extra packages)
	case "~E":
		return d.processFlexArray(m, d.parseFlex20Position, t, ds, buf)
	case "~X":
		return d.processFlexSingle(m, d.parseFlex20Position, t, ds, buf)
	}
	return nil
}

// Decode decodes one message.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.GetByte(buf.ReaderIndex()) == '@' {
		return d.decodeNtcb(m, buf)
	}
	return d.decodeFlex(m, buf)
}

// encode is NavisProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	if c.Type == model.CommandCustom {
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException")
		}
		return encodeContent(0, 1, kit.NettyBytes(*data))
	}
	return nil
}
