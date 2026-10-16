// Package adm is the ADM protocol (AdmProtocol): binary little-endian reports over TCP,
// optionally prefixed with the IMEI, and text command responses.
package adm

import (
	"strconv"
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
		Name:         "adm",
		DataCommands: protocol.DataCommands("adm"),
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

func isDigit(b int) bool { return b >= '0' && b <= '9' }

// Framer is AdmFrameDecoder: the length byte follows the device id, after the IMEI when
// the frame starts with one.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 15+3 {
			return nil
		}
		var length int
		if isDigit(buf.GetUnsignedByte(buf.ReaderIndex())) {
			length = 15 + buf.GetUnsignedByte(buf.ReaderIndex()+15+2)
		} else {
			length = buf.GetUnsignedByte(buf.ReaderIndex() + 2)
		}
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

const (
	cmdResponseSize = 0x84
	msgIMEI         = 0x03
)

func decodeData(m *ingest.Message, buf *kit.Buf, typ int) any {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	if typ&3 != 0 {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
	pos.Set(model.KeyIndex, buf.ReadUnsignedShortLE())

	status := buf.ReadUnsignedShortLE()
	pos.Set(model.KeyStatus, status)
	pos.Valid = !kit.Check(int64(status), 5)
	pos.SetLatitude(float64(buf.ReadFloatLE()))
	pos.SetLongitude(float64(buf.ReadFloatLE()))
	pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0
	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()) / 10.0)

	pos.Set(model.KeyAcceleration, float64(buf.ReadUnsignedByte())/10.0)
	pos.Altitude = float64(buf.ReadShortLE())
	pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
	pos.Set(model.KeySatellites, buf.ReadUnsignedByte()&0x0f)

	pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))

	pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)

	if kit.Check(int64(typ), 2) {
		buf.ReadUnsignedByte() // vib
		buf.ReadUnsignedByte() // vib_count
		out := buf.ReadUnsignedByte()
		for i := 0; i <= 3; i++ {
			v := 0
			if kit.Check(int64(out), i) {
				v = 1
			}
			pos.Set(model.PrefixOut+strconv.Itoa(i+1), v)
		}
		buf.ReadUnsignedByte() // in_alarm
	}

	if kit.Check(int64(typ), 3) {
		for i := 1; i <= 6; i++ {
			pos.Set(model.PrefixAdc+strconv.Itoa(i), float64(buf.ReadUnsignedShortLE())/1000.0)
		}
	}

	if kit.Check(int64(typ), 4) {
		for i := 1; i <= 2; i++ {
			pos.Set(model.PrefixCount+strconv.Itoa(i), buf.ReadUnsignedIntLE())
		}
	}

	if kit.Check(int64(typ), 5) {
		for i := 1; i <= 3; i++ {
			pos.Set("fuel"+strconv.Itoa(i), buf.ReadUnsignedShortLE())
		}
		for i := 1; i <= 3; i++ {
			pos.Set(model.PrefixTemp+strconv.Itoa(i), buf.ReadUnsignedByte())
		}
	}

	if kit.Check(int64(typ), 6) {
		endIndex := buf.ReaderIndex() + buf.ReadUnsignedByte()
		for buf.ReaderIndex() < endIndex {
			mask := buf.ReadUnsignedByte()
			var value int64
			switch mask >> 6 {
			case 3:
				value = buf.ReadLongLE()
			case 2:
				value = buf.ReadUnsignedIntLE()
			case 1:
				value = int64(buf.ReadUnsignedShortLE())
			default:
				value = int64(buf.ReadUnsignedByte())
			}
			switch index := mask & 0x3f; index {
			case 1:
				pos.Set(model.PrefixTemp+"1", value)
			case 2:
				pos.Set(model.KeyHumidity, value)
			case 3:
				pos.Set("illumination", value)
			case 4:
				pos.Set(model.KeyBattery, value)
			default:
				pos.Set("can"+strconv.Itoa(index), value)
			}
		}
	}

	if kit.Check(int64(typ), 7) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	}
	return pos
}

func parseCommandResponse(m *ingest.Message, buf *kit.Buf) any {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	length := buf.BytesBefore(0)
	if length < 0 {
		length = cmdResponseSize - 3
	}
	pos.Set(model.KeyResult, kit.UTF8(buf.ReadSlice(length).Bytes()))
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if isDigit(buf.GetUnsignedByte(buf.ReaderIndex())) {
		m.Session(kit.UTF8(buf.ReadSlice(15).Bytes()))
	}
	buf.ReadUnsignedShortLE() // device id
	size := buf.ReadUnsignedByte()
	if size == cmdResponseSize {
		return parseCommandResponse(m, buf)
	}
	typ := buf.ReadUnsignedByte()
	if typ == msgIMEI {
		m.Session(kit.UTF8(buf.ReadSlice(15).Bytes()))
		return nil
	}
	return decodeData(m, buf, typ)
}

// encode is AdmProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandGetDeviceStatus:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "STATUS\r\n")
	case model.CommandCustom:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "%s\r\n", model.CommandKeyData)
	}
	return nil
}
