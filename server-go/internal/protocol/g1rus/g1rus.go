// Package g1rus is the G1rus protocol (G1rusProtocol): binary messages with 0x1B escapes,
// regular reports alone or mixed with other records.
package g1rus

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
		Name: "g1rus",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is G1rusFrameDecoder: everything received, unescaped (1B 00 is 1B, 1B xx is F8).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if !buf.IsReadable() {
			return nil
		}
		result := kit.Empty()
		for buf.IsReadable() {
			b := buf.ReadUnsignedByte()
			if b == 0x1B {
				if buf.ReadUnsignedByte() == 0x00 {
					result.WriteByte8(0x1B)
				} else {
					result.WriteByte8(0xF8)
				}
			} else {
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

const (
	msgRegular = 1
	msgMixed   = 4
)

func check(v, i int) bool { return kit.Check(int64(v), i) }

// readString is a string after its length in the low 4 bits of a byte.
func readString(buf *kit.Buf) string {
	return buf.ReadString(buf.ReadUnsignedByte() & 0xF)
}

func decodeRegular(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, msgType int) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(time.UnixMilli((buf.ReadUnsignedIntLE() + 946684800) * 1000))

	if check(msgType, 6) {
		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
	}

	dataMask := buf.ReadUnsignedShort()

	if check(dataMask, 0) {
		buf.ReadUnsignedByte() // length
		readString(buf)        // device name
		pos.Set(model.KeyVersionFw, readString(buf))
		pos.Set(model.KeyVersionHw, readString(buf))
	}

	if check(dataMask, 1) {
		buf.ReadUnsignedByte() // length
		locationMask := buf.ReadUnsignedShort()
		if check(locationMask, 0) {
			validity := buf.ReadUnsignedByte()
			pos.Set(model.KeySatellites, kit.ToInt(validity, 5))
			pos.Valid = kit.BetweenInt(validity, 5, 7) == 2
		}
		if check(locationMask, 1) {
			pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		}
		if check(locationMask, 2) {
			pos.Speed = float64(buf.ReadUnsignedShort())
		}
		if check(locationMask, 3) {
			pos.Course = float64(buf.ReadUnsignedShort())
		}
		if check(locationMask, 4) {
			pos.Altitude = float64(buf.ReadShort())
		}
		if check(locationMask, 5) {
			pos.Set(model.KeyHdop, buf.ReadUnsignedShort())
		}
		if check(locationMask, 6) {
			pos.Set(model.KeyVdop, buf.ReadUnsignedShort())
		}
	}

	if check(dataMask, 2) {
		buf.Skip(buf.ReadUnsignedByte())
	}
	if check(dataMask, 3) {
		buf.Skip(buf.ReadUnsignedByte())
	}

	if check(dataMask, 4) {
		buf.ReadUnsignedByte() // length
		pos.Set(model.KeyPower, buf.ReadUnsignedShort()*110/4096-10)
		pos.Set(model.KeyBattery, buf.ReadUnsignedShort()*110/4096-10)
		pos.Set(model.KeyDeviceTemp, buf.ReadUnsignedShort()*110/4096-10)
	}

	if check(dataMask, 5) {
		buf.Skip(buf.ReadUnsignedByte())
	}
	if check(dataMask, 7) {
		buf.Skip(buf.ReadUnsignedByte())
	}

	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header
	buf.ReadUnsignedByte() // version

	msgType := buf.ReadUnsignedByte()
	imei := strconv.FormatInt(buf.ReadLong(), 10)
	buf.SetReaderIndex(buf.ReaderIndex() - 1)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	switch kit.ToInt(msgType, 6) {
	case msgRegular:
		return decodeRegular(m, ds, buf, msgType)
	case msgMixed:
		positions := []*model.Position{}
		for buf.ReadableBytes() > 5 {
			length := buf.ReadUnsignedShort()
			subtype := buf.ReadUnsignedByte()
			if kit.ToInt(subtype, 6) == msgRegular {
				positions = append(positions, decodeRegular(m, ds, buf, subtype))
			} else {
				buf.Skip(length - 1)
			}
		}
		if len(positions) == 0 {
			return nil
		}
		return positions
	}

	buf.ReadUnsignedShort() // checksum
	buf.ReadUnsignedByte()  // tail

	return nil
}
