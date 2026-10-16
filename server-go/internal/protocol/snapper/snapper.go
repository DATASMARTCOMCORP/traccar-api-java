// Package snapper is the Snapper protocol (SnapperProtocol): binary packets with JSON GPS
// data over TCP.
package snapper

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "snapper",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is SnapperFrameDecoder: a 2-byte ping ('P') or a packet with its length.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.GetByte(buf.ReaderIndex()) == 'P' {
			if buf.ReadableBytes() >= 2 {
				return buf.ReadSlice(2)
			}
		} else if buf.ReadableBytes() >= 16 {
			length := int(buf.GetIntLE(buf.ReaderIndex()+12)) + 12 + 4 + 9
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		}
		return nil
	}}
}

const (
	msgHello                = 0x01
	msgSendEvents           = 0x21
	msgSendTechInfo         = 0x23
	msgSendGpsData          = 0x32
	msgSendConcatenatedData = 0x37
)

var dateFormat = kit.NewDateFormat("ddMMyyHHmmss", nil)

func sendResponse(m *ingest.Message, index, typ int, answer string) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteByte8('K')
	response.WriteByte8(3)   // protocol version
	response.WriteLongLE(0)  // reserved
	response.WriteShortLE(0) // encryption
	response.WriteIntLE(int64(kit.Len(answer)))
	response.WriteIntLE(0) // reserved
	response.WriteShortLE(index)
	response.WriteByte8(kit.Sum(kit.ASCIIBytes(answer)))
	response.WriteShortLE(typ)
	response.WriteASCII(answer)
	m.Send(response)
}

func decodeEvents(pos *model.Position, buf *kit.Buf) {
	pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
	buf.ReadUnsignedByte()  // info 1
	buf.ReadUnsignedByte()  // info 2
	buf.ReadUnsignedIntLE() // timestamp
	buf.ReadUnsignedByte()  // timezone
}

func decodeTechInfo(pos *model.Position, buf *kit.Buf) {
	for i := 0; i < 5; i++ {
		buf.ReadUnsignedByte() // index
		switch buf.ReadUnsignedByte() {
		case 0x00:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte())/10.0)
			pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		case 0x01:
			pos.Set("interiorTemp", int(buf.ReadSignedByte()))
			pos.Set(model.KeyEngineTemp, int(buf.ReadSignedByte()))
			buf.ReadUnsignedByte() // reserved
		default:
			buf.Skip(3)
		}
	}
}

func decodeGpsData(pos *model.Position, buf *kit.Buf) {
	json := kit.ParseJSONObject([]byte(kit.UTF8([]byte(buf.ReadString(buf.ReadableBytes())))))
	flags := int64(kit.IntRadix(json.String("f"), 16))
	if !kit.Check(flags, 3) {
		return
	}
	pos.Valid = kit.Check(flags, 1)
	if !kit.Check(flags, 6) {
		pos.SetLatitude(-pos.Latitude)
	}
	if !kit.Check(flags, 7) {
		pos.SetLongitude(-pos.Longitude)
	}
	pos.SetTime(dateFormat.Parse(json.String("d") + kit.SplitRegex(json.String("t"), `\.`)[0]))
	lat := json.String("la")
	pos.SetLatitude(float64(kit.Int(kit.Substring(lat, 0, 2))) + kit.Double(kit.SubstringFrom(lat, 2))/60)
	lon := json.String("lo")
	pos.SetLongitude(float64(kit.Int(kit.Substring(lon, 0, 3))) + kit.Double(kit.SubstringFrom(lon, 3))/60)
	pos.Altitude = kit.Double(json.String("a"))
	pos.Speed = kit.Double(json.String("s"))
	pos.Course = kit.Double(json.String("c"))
	pos.Set(model.KeySatellites, kit.Int(json.String("sv")))
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.ReadSignedByte() == 'P' {
		if m.HasChannel() {
			m.Send(kit.NewBuf([]byte{0x50, 0x4f}))
		}
		return nil
	}
	buf.ReadUnsignedByte()  // protocol version
	buf.ReadUnsignedIntLE() // system bonus identifier
	ds := m.Session(strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
	if ds == nil {
		return nil
	}
	buf.ReadUnsignedShortLE() // encryption
	length := int(buf.ReadIntLE())
	buf.ReadUnsignedByte()     // flags
	buf.ReadUnsignedMediumLE() // reserved
	index := buf.ReadUnsignedShortLE()
	buf.ReadUnsignedByte() // checksum
	typ := buf.ReadUnsignedShortLE()

	if typ == msgHello {
		sendResponse(m, index, typ, "hello")
	} else {
		sendResponse(m, index, typ, "OK")
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	switch typ {
	case msgSendEvents:
		decodeEvents(pos, buf)
		m.LastLocation(pos, nil)
		return pos
	case msgSendTechInfo:
		decodeTechInfo(pos, buf)
		m.LastLocation(pos, nil)
		return pos
	case msgSendGpsData:
		decodeGpsData(pos, buf.ReadSlice(length))
		return pos
	case msgSendConcatenatedData:
		for count := buf.ReadUnsignedShortLE(); count > 0; count-- {
			partType := buf.ReadUnsignedShortLE()
			partLength := buf.ReadUnsignedShortLE()
			switch partType {
			case msgSendEvents:
				decodeEvents(pos, buf)
			case msgSendTechInfo:
				decodeTechInfo(pos, buf)
			case msgSendGpsData:
				decodeGpsData(pos, buf.ReadSlice(partLength))
			default:
				buf.Skip(partLength)
			}
		}
		if pos.FixTime.IsZero() {
			m.LastLocation(pos, nil)
		}
		return pos
	}
	return nil
}
