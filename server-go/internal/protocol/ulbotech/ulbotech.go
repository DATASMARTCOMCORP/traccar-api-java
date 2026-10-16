// Package ulbotech is the Ulbotech protocol (UlbotechProtocol): escaped binary frames
// between 0xF8 bytes, and "*TS...#" text replies, over TCP.
package ulbotech

import (
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
		Name:         "ulbotech",
		DataCommands: protocol.DataCommands("ulbotech"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Framer is UlbotechFrameDecoder: binary frames from 0xF8 to 0xF8 (unescaped: F7 00 is
// F7, F7 0F is F8), text frames up to '#'.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}
		if buf.GetUnsignedByte(buf.ReaderIndex()) == 0xF8 {
			index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), 0xF8)
			if index != -1 {
				result := kit.Empty()
				for buf.ReaderIndex() <= index {
					b := buf.ReadUnsignedByte()
					if b == 0xF7 {
						switch buf.ReadUnsignedByte() {
						case 0x00:
							result.WriteByte8(0xF7)
						case 0x0F:
							result.WriteByte8(0xF8)
						}
					} else {
						result.WriteByte8(b)
					}
				}
				return result
			}
		} else {
			index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '#')
			if index != -1 {
				return buf.ReadSlice(index + 1 - buf.ReaderIndex())
			}
		}
		return nil
	}}
}

func javaString(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// encode is UlbotechProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	if c.Type == model.CommandCustom {
		return kit.NewBuf(kit.NettyBytes("*TS01," + javaString(kit.CommandString(c, model.CommandKeyData)) + "#"))
	}
	return nil
}

const (
	dataGPS         = 0x01
	dataLBS         = 0x02
	dataStatus      = 0x03
	dataOdometer    = 0x04
	dataADC         = 0x05
	dataGeofence    = 0x06
	dataOBD2        = 0x07
	dataFuel        = 0x08
	dataOBD2Alarm   = 0x09
	dataHarshDriver = 0x0A
	dataCANBus      = 0x0B
	dataJ1708       = 0x0C
	dataVIN         = 0x0D
	dataRFID        = 0x0E
	dataEvent       = 0x10
)

func decodeObd(pos *model.Position, buf *kit.Buf, length int) {
	end := buf.ReaderIndex() + length
	for buf.ReaderIndex() < end {
		parameterLength := buf.GetUnsignedByte(buf.ReaderIndex()) >> 4
		mode := buf.ReadUnsignedByte() & 0x0F
		kit.ObdDecode(mode, buf.ReadSlice(parameterLength-1).HexDump()).Add(pos)
	}
}

func decodeJ1708(pos *model.Position, buf *kit.Buf, length int) {
	end := buf.ReaderIndex() + length
	for buf.ReaderIndex() < end {
		mark := buf.ReadUnsignedByte()
		l := kit.BetweenInt(mark, 0, 6)
		typ := kit.BetweenInt(mark, 6, 8)
		id := buf.ReadUnsignedByte()
		if typ == 3 {
			id += 256
		}
		value := buf.ReadSlice(l - 1).HexDump()
		if typ == 2 || typ == 3 {
			pos.Set(kit.Format("pid%d", id), value)
		}
	}
}

var driverBehavior = []string{
	"rapidAcceleration", "roughBraking", "harshCourse", "noWarmUp",
	"longIdle", "fatigueDriving", "roughTerrain", "highRpm",
}

func decodeDriverBehavior(pos *model.Position, buf *kit.Buf) {
	value := int64(buf.ReadUnsignedByte())
	for i, key := range driverBehavior {
		if kit.Check(value, i) {
			pos.Set(key, true)
		}
	}
}

func decodeAlarm(alarm int64) string {
	switch {
	case kit.Check(alarm, 0):
		return model.AlarmPowerOff
	case kit.Check(alarm, 1):
		return model.AlarmMovement
	case kit.Check(alarm, 2):
		return model.AlarmOverspeed
	case kit.Check(alarm, 4):
		return model.AlarmGeofence
	case kit.Check(alarm, 10):
		return model.AlarmSos
	}
	return ""
}

func decodeAdc(pos *model.Position, buf *kit.Buf, length int) {
	for range length / 2 {
		value := buf.ReadUnsignedShort()
		id := kit.FromInt(value, 12)
		value = kit.ToInt(value, 12)
		switch id {
		case 0:
			pos.Set(model.KeyPower, float64(value*(100+10))/4096.0-10)
		case 1:
			pos.Set(model.PrefixTemp+"1", float64(value*(125+55))/4096.0-55)
		case 2:
			pos.Set(model.KeyBattery, float64(value*(100+10))/4096.0-10)
		case 3:
			pos.Set(model.PrefixAdc+"1", float64(value*(100+10))/4096.0-10)
		case 5, 6, 7, 8, 9:
			pos.Set(kit.Format("fuel%d", id-4), float64(value*2000)/4096.0)
		default:
			pos.Set(kit.Format("%s%d", model.PrefixIo, id), value)
		}
	}
}

var pattern = kit.NewPattern().
	Text("*TS").
	Number("dd,").           // protocol version
	Number("(d{15}),").      // device id
	Number("(dd)(dd)(dd)").  // time
	Number("(dd)(dd)(dd),"). // date
	Expression("([^#]+)").   // command
	Text("#").
	Compile()

func decodeText(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	t := b.Date()
	m.LastLocation(pos, &t)

	pos.Set(model.KeyResult, parser.Next())
	return pos
}

func decodeBinary(m *ingest.Message, buf *kit.Buf) any {
	buf.ReadUnsignedByte() // header
	buf.ReadUnsignedByte() // version
	buf.ReadUnsignedByte() // type

	imei := kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	zone, _ := ds.Get("timezone").(*time.Location)
	if zone == nil {
		z, err := m.TimeZone(ds.DeviceID, "")
		if err != nil {
			panic(err)
		}
		zone = z
		ds.Set("timezone", zone)
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	seconds := buf.ReadUnsignedInt() & 0x7fffffff
	seconds += 946684800 // 2000-01-01 00:00
	seconds -= int64(javatime.RawOffset(zone, m.Now))
	t := time.UnixMilli(seconds * 1000)

	hasLocation := false

	for buf.ReadableBytes() > 3 {
		typ := buf.ReadUnsignedByte()
		var length int
		if typ == dataCANBus {
			length = buf.ReadUnsignedShort()
		} else {
			length = buf.ReadUnsignedByte()
		}

		switch typ {
		case dataGPS:
			hasLocation = true
			pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
			pos.Course = float64(buf.ReadUnsignedShort())
			hdop := buf.ReadUnsignedShort()
			pos.Valid = hdop < 9999
			pos.Set(model.KeyHdop, float64(hdop)/100.0)
		case dataLBS:
			mcc := buf.ReadUnsignedShort()
			mnc := buf.ReadUnsignedShort()
			lac := buf.ReadUnsignedShort()
			var cid int64
			if length == 11 {
				cid = buf.ReadUnsignedInt()
			} else {
				cid = int64(buf.ReadUnsignedShort())
			}
			pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, cid, -buf.ReadUnsignedByte()))
			if length > 9 && length != 11 {
				buf.Skip(length - 9)
			}
		case dataStatus:
			status := buf.ReadUnsignedShort()
			pos.Set(model.KeyIgnition, kit.Check(int64(status), 9))
			pos.Set(model.KeyStatus, status)
			pos.AddAlarm(decodeAlarm(int64(buf.ReadUnsignedShort())))
		case dataOdometer:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		case dataADC:
			decodeAdc(pos, buf, length)
		case dataGeofence:
			pos.Set("geofenceIn", buf.ReadUnsignedInt())
			pos.Set("geofenceAlarm", buf.ReadUnsignedInt())
		case dataOBD2, dataOBD2Alarm:
			decodeObd(pos, buf, length)
		case dataFuel:
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedInt())/10000.0)
		case dataHarshDriver:
			decodeDriverBehavior(pos, buf)
		case dataCANBus:
			pos.Set("can", buf.ReadSlice(length).HexDump())
		case dataJ1708:
			decodeJ1708(pos, buf, length)
		case dataVIN:
			pos.Set(model.KeyVin, buf.ReadString(length))
		case dataRFID:
			pos.Set(model.KeyDriverUniqueID, buf.ReadString(length-1))
			pos.Set("authorized", buf.ReadUnsignedByte() != 0)
		case dataEvent:
			pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
			if length > 1 {
				pos.Set("eventMask", buf.ReadUnsignedInt())
			}
		default:
			buf.Skip(length)
		}
	}

	if !hasLocation {
		m.LastLocation(pos, &t)
	} else {
		pos.SetTime(t)
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.GetUnsignedByte(buf.ReaderIndex()) == 0xF8 {
		response := kit.Empty()
		response.WriteByte8(0xF8)
		response.WriteByte8(dataGPS)
		response.WriteByte8(0xFE)
		response.WriteShort(buf.GetShort(response.WriterIndex() - 1 - 2))
		response.WriteShort(kit.CRC16(kit.CRC16_XMODEM, response.Array()[1:5]))
		response.WriteByte8(0xF8)
		m.Send(response)

		return decodeBinary(m, buf)
	}

	crc := kit.CRC16(kit.CRC16_XMODEM, buf.Array()[1:1+buf.WriterIndex()-2])
	m.Send(kit.NewBuf(kit.NettyBytes(kit.Format("*TS01,ACK:%04X#", crc))))

	return decodeText(m, kit.Latin1(buf.Bytes()))
}
