// Package omnicomm is the Omnicomm protocol (OmnicommProtocol): binary frames (0xC0, with
// byte stuffing) whose archive records are protobuf OmnicommMessages.
package omnicomm

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "omnicomm",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is OmnicommFrameDecoder: the length is at offset 2 (LE, plus 6 bytes of header
// and checksum); 0xDB 0xDC and 0xDB 0xDD unescape to 0xC0 and 0xDB.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 6 {
			return nil
		}
		endIndex := buf.GetUnsignedShortLE(buf.ReaderIndex()+2) + buf.ReaderIndex() + 6
		if buf.WriterIndex() < endIndex {
			return nil
		}
		result := kit.Empty()
		result.WriteByte8(buf.ReadUnsignedByte())
		for buf.ReaderIndex() < endIndex {
			b := buf.ReadUnsignedByte()
			if b == 0xDB {
				switch buf.ReadUnsignedByte() {
				case 0xDC:
					result.WriteByte8(0xC0)
				case 0xDD:
					result.WriteByte8(0xDB)
				}
				endIndex++
			} else {
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

const (
	msgIdentification     = 0x80
	msgArchiveInquiry     = 0x85
	msgArchiveData        = 0x86
	msgRemoveArchiveQuery = 0x87
)

func sendResponse(m *ingest.Message, kind int, index int64) {
	response := kit.Empty()
	response.WriteByte8(0xC0)
	response.WriteByte8(kind)
	response.WriteShortLE(4)
	response.WriteIntLE(index)
	response.WriteShortLE(kit.CRC16(kit.CRC16_CCITT_FALSE, response.Bytes()[1:]))
	m.Send(response)
}

// omnicommEpoch is 2009-01-01 00:00 UTC in seconds.
const omnicommEpoch = 1230768000

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // prefix
	kind := buf.ReadUnsignedByte()
	buf.ReadUnsignedShortLE() // length

	switch kind {
	case msgIdentification:
		m.Session(strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
		sendResponse(m, msgArchiveInquiry, 0)

	case msgArchiveData:
		ds := m.Session()
		if ds == nil {
			return nil
		}

		index := buf.ReadUnsignedIntLE()
		buf.ReadUnsignedIntLE() // time
		buf.ReadUnsignedByte()  // priority

		positions := []*model.Position{}

		for buf.ReadableBytes() > 2 {
			length := buf.ReadUnsignedShortLE()
			message := parseMessage(buf.ReadBytes(length))

			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			if data := message.groups[2]; data != nil { // General
				pos.Set(model.KeyPower, float64(data.int32(8))/10.0)
				pos.Set(model.KeyBatteryLevel, data.int32(9))
				pos.Set(model.KeyIgnition, kit.Check(int64(data.int32(4)), 0))
				pos.Set(model.KeyRpm, data.int32(7))
			}

			if data := message.groups[5]; data != nil { // NAV
				pos.Valid = true
				seconds := int32(data.int32(7)) + omnicommEpoch // int arithmetic
				pos.SetTime(time.UnixMilli(int64(seconds) * 1000))
				pos.SetLatitude(float64(data.sint32(1)) / 10000000.0)
				pos.SetLongitude(float64(data.sint32(2)) / 10000000.0)
				pos.Speed = units.KnotsFromKph(float64(data.int32(3)) / 10.0)
				pos.Course = float64(data.int32(4))
				pos.Altitude = float64(data.sint32(6)) / 10.0
				pos.Set(model.KeySatellites, data.int32(5))
			}

			if data := message.groups[8]; data != nil { // LLSDt
				pos.Set("fuel1Temp", data.sint32(1))
				pos.Set("fuel1", data.int32(2))
				pos.Set("fuel1State", data.sint32(3))
			}

			if !pos.FixTime.IsZero() {
				positions = append(positions, pos)
			}
		}

		if len(positions) == 0 {
			sendResponse(m, msgRemoveArchiveQuery, index+1)
			return nil
		}
		return positions
	}

	return nil
}
