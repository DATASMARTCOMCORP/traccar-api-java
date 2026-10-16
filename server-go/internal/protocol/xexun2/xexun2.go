// Package xexun2 is the Xexun2 protocol (Xexun2Protocol): binary frames between 0xfaaf
// flags, with 0xfbbf escapes, over TCP.
package xexun2

import (
	"math"
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

const (
	flag        = 0xfaaf
	msgCommand  = 0x07
	msgPosition = 0x14
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "xexun2",
		DataCommands: protocol.DataCommands("xexun2"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:       Framer(),
				Decoder:      ingest.DecoderFunc(Decode),
				Encoder:      ingest.CommandEncoderFunc(encode),
				FrameEncoder: FrameEncode,
			}
		}}},
	}
}

// Framer is Xexun2FrameDecoder: a frame ends at the next 0xfaaf flag (searched from two
// bytes past the start); 0xfbbf 0x01 and 0xfbbf 0x02 unescape to 0xfaaf and 0xfbbf.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 5 {
			return nil
		}
		index := -1
		for i := buf.ReaderIndex() + 2; i+1 < buf.WriterIndex(); i++ {
			if buf.GetUnsignedByte(i) == 0xfa && buf.GetUnsignedByte(i+1) == 0xaf {
				index = i
				break
			}
		}
		if index < 0 {
			return nil
		}
		result := kit.Empty()
		for buf.ReaderIndex() < index+2 {
			b := buf.ReadUnsignedByte()
			if b == 0xfb && buf.IsReadable() && buf.GetUnsignedByte(buf.ReaderIndex()) == 0xbf {
				buf.ReadUnsignedByte()
				switch buf.ReadUnsignedByte() {
				case 0x01:
					result.WriteByte8(0xfa)
					result.WriteByte8(0xaf)
				case 0x02:
					result.WriteByte8(0xfb)
					result.WriteByte8(0xbf)
				}
			} else {
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

// FrameEncode is Xexun2FrameEncoder: it escapes 0xfaaf and 0xfbbf between the first and
// the last two bytes.
func FrameEncode(msg []byte) []byte {
	in := kit.NewBuf(msg)
	out := kit.Empty()
	out.WriteBytes(in.ReadBytes(2))
	for in.ReadableBytes() > 2 {
		b := in.ReadUnsignedByte()
		switch {
		case b == 0xfa && in.IsReadable() && in.GetUnsignedByte(in.ReaderIndex()) == 0xaf:
			in.ReadUnsignedByte()
			out.WriteBytes([]byte{0xfb, 0xbf, 0x01})
		case b == 0xfb && in.IsReadable() && in.GetUnsignedByte(in.ReaderIndex()) == 0xbf:
			in.ReadUnsignedByte()
			out.WriteBytes([]byte{0xfb, 0xbf, 0x02})
		default:
			out.WriteByte8(b)
		}
	}
	out.WriteBytes(in.ReadBytes(2))
	return out.Bytes()
}

func sendResponse(m *ingest.Message, typ, index int, imei *kit.Buf) {
	response := kit.Empty()
	response.WriteShort(flag)
	response.WriteShort(typ)
	response.WriteShort(index)
	response.WriteBytes(imei.Bytes())
	response.WriteShort(1)      // attributes / length
	response.WriteShort(0xfffe) // checksum
	response.WriteByte8(1)      // response
	response.WriteShort(flag)
	m.Send(response)
}

func decodeAlarm(value int64) string {
	switch {
	case kit.Check(value, 0):
		return model.AlarmSos
	case kit.Check(value, 1):
		return model.AlarmRemoving
	case kit.Check(value, 15):
		return model.AlarmFallDown
	}
	return ""
}

// javaInt is Java's (int) cast of a double.
func javaInt(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case v >= math.MaxInt32:
		return math.MaxInt32
	case v <= math.MinInt32:
		return math.MinInt32
	}
	return math.Trunc(v)
}

func convertCoordinate(value float64) float64 {
	degrees := javaInt(value / 100)
	minutes := value - degrees*100
	return degrees + minutes/60
}

var macPairs = regexp.MustCompile("(..)")

// Decode is Xexun2ProtocolDecoder.
func Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // flag
	typ := buf.ReadUnsignedShort()
	index := buf.ReadUnsignedShort()

	imei := buf.ReadSlice(8)
	ds := m.Session(kit.Substring(imei.HexDump(), 0, 15))
	if ds == nil {
		return nil
	}

	payloadSize := buf.ReadUnsignedShort() & 0x03ff
	checksum := buf.ReadUnsignedShort()

	if checksum != kit.IP(buf.GetBytes(buf.ReaderIndex(), payloadSize)) {
		return nil
	}

	if typ != msgCommand {
		sendResponse(m, typ, index, imei)
	}

	if typ != msgPosition {
		return nil
	}

	count := buf.ReadUnsignedByte()
	lengths := make([]int, count)
	for i := range lengths {
		lengths[i] = buf.ReadUnsignedShort()
	}

	positions := []*model.Position{}
	for i := 0; i < count; i++ {
		endIndex := buf.ReaderIndex() + lengths[i]

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyIndex, buf.ReadUnsignedByte())

		pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())

		battery := buf.ReadUnsignedShort()
		pos.Set(model.KeyCharge, kit.Check(int64(battery), 15))
		pos.Set(model.KeyBatteryLevel, kit.ToInt(battery, 15))

		mask := int64(buf.ReadUnsignedByte())

		if kit.Check(mask, 0) {
			pos.AddAlarm(decodeAlarm(buf.ReadUnsignedInt()))
		}
		if kit.Check(mask, 1) {
			positionMask := int64(buf.ReadUnsignedByte())
			if kit.Check(positionMask, 0) {
				pos.Valid = true
				pos.FixTime = pos.DeviceTime
				pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
				pos.SetLongitude(convertCoordinate(float64(buf.ReadFloat())))
				pos.SetLatitude(convertCoordinate(float64(buf.ReadFloat())))
			}
			network := model.NewNetwork()
			if kit.Check(positionMask, 1) {
				wifiCount := buf.ReadUnsignedByte()
				for j := 0; j < wifiCount; j++ {
					mac := macPairs.ReplaceAllString(buf.ReadSlice(6).HexDump(), "$1:")
					network.AddWifiAccessPoint(model.NewWifiAccessPoint(
						mac[:len(mac)-1], int(buf.ReadSignedByte())))
				}
			}
			if kit.Check(positionMask, 2) {
				cellCount := buf.ReadUnsignedByte()
				for j := 0; j < cellCount; j++ {
					mcc := buf.ReadUnsignedShort()
					mnc := buf.ReadUnsignedShort()
					lac := int(buf.ReadInt())
					cid := buf.ReadUnsignedInt()
					network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, int(buf.ReadSignedByte())))
				}
			}
			if network.WifiAccessPoints != nil || network.CellTowers != nil {
				pos.Network = network
			}
			if kit.Check(positionMask, 3) {
				buf.Skip(12 * buf.ReadUnsignedByte()) // tof
			}
			if kit.Check(positionMask, 5) {
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)
				pos.Course = float64(buf.ReadUnsignedShort()) / 10.0
			}
			if kit.Check(positionMask, 6) {
				pos.Valid = true
				pos.FixTime = pos.DeviceTime
				pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
				pos.SetLongitude(convertCoordinate(buf.ReadDouble()))
				pos.SetLatitude(convertCoordinate(buf.ReadDouble()))
			}
			if kit.Check(positionMask, 7) {
				dataLength := buf.ReadUnsignedShort()
				if dataLength > 0 {
					dataType := buf.ReadUnsignedByte()
					dataEndIndex := buf.ReaderIndex() + buf.ReadUnsignedShort()
					if dataType == 'G' {
						pos.FixTime = pos.DeviceTime
						pos.SetLongitude(convertCoordinate(buf.ReadDouble()))
						pos.SetLatitude(convertCoordinate(buf.ReadDouble()))
						pos.Valid = buf.ReadUnsignedByte() > 0
						pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
						buf.ReadUnsignedByte() // satellite signal-to-noise ratio
						pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)
						pos.Course = float64(buf.ReadUnsignedShort()) / 10.0
						pos.Altitude = float64(buf.ReadFloat())
					}
					buf.SetReaderIndex(dataEndIndex)
				}
			}
		}
		if kit.Check(mask, 3) {
			buf.ReadUnsignedInt() // fingerprint
		}
		if kit.Check(mask, 4) {
			buf.Skip(20) // version
			buf.Skip(8)  // imsi
			buf.Skip(10) // iccid
		}
		if kit.Check(mask, 5) {
			buf.Skip(12) // device parameters
		}

		if !pos.Valid {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}
		positions = append(positions, pos)

		buf.SetReaderIndex(endIndex)
	}
	return positions
}

func encodeContent(uniqueID, content string) *kit.Buf {
	message := kit.ASCIIBytes(content)
	buf := kit.Empty()
	buf.WriteShort(flag)
	buf.WriteShort(msgCommand)
	buf.WriteShort(1) // index
	buf.WriteBytes(kit.ParseHex(uniqueID + "0"))
	buf.WriteShort(len(message))
	buf.WriteShort(kit.IP(message))
	buf.WriteBytes(message)
	buf.WriteShort(flag)
	return buf
}

// encode is Xexun2ProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	uniqueID := m.UniqueID(c.DeviceID)
	switch c.Type {
	case model.CommandCustom:
		return encodeContent(uniqueID, *kit.CommandString(c, model.CommandKeyData))
	case model.CommandPositionPeriodic:
		frequency := kit.CommandInt(c, model.CommandKeyFrequency)
		return encodeContent(uniqueID, kit.Format("tracking_send=%1$d,%1$d", frequency))
	case model.CommandPowerOff:
		return encodeContent(uniqueID, "of=1")
	case model.CommandRebootDevice:
		return encodeContent(uniqueID, "reset")
	}
	return nil
}
