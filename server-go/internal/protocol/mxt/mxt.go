// Package mxt is the MXT protocol (MxtProtocol): binary frames between 0x01 and 0x04,
// with 0x10 escapes, each position acknowledged.
package mxt

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "mxt",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is MxtFrameDecoder: a frame ends with 0x04; 0x10 escapes the next byte (minus
// 0x20).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}
		index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), 0x04)
		if index != -1 {
			result := kit.Empty()
			for buf.ReaderIndex() <= index {
				b := buf.ReadUnsignedByte()
				if b == 0x10 {
					result.WriteByte8(buf.ReadUnsignedByte() - 0x20)
				} else {
					result.WriteByte8(b)
				}
			}
			return result
		}
		return nil
	}}
}

const (
	msgAck      = 0x02
	msgPosition = 0x31
)

func sendResponse(m *ingest.Message, device int, id int64, crc int) {
	response := kit.Empty()
	response.WriteByte8(device)
	response.WriteByte8(msgAck)
	response.WriteIntLE(id)
	response.WriteShortLE(crc)
	response.WriteShortLE(kit.CRC16(kit.CRC16_XMODEM, response.Bytes()))

	encoded := kit.Empty()
	encoded.WriteByte8(0x01) // header
	for response.IsReadable() {
		b := int(response.ReadSignedByte())
		if b == 0x01 || b == 0x04 || b == 0x10 || b == 0x11 || b == 0x13 {
			encoded.WriteByte8(0x10)
			b += 0x20
		}
		encoded.WriteByte8(b)
	}
	encoded.WriteByte8(0x04) // ending
	m.Send(encoded)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // start
	device := buf.ReadUnsignedByte()
	t := buf.ReadUnsignedByte()

	id := buf.ReadUnsignedIntLE()
	ds := m.Session(strconv.FormatInt(id, 10))
	if ds == nil {
		return nil
	}

	if t != msgPosition {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedByte() // protocol
	infoGroups := int64(buf.ReadUnsignedByte())

	pos.Set(model.KeyIndex, buf.ReadUnsignedShortLE())

	b := kit.NewDateBuilder(nil).SetDate(2000, 1, 1)

	date := buf.ReadUnsignedIntLE()
	days := kit.From(date, 6+6+5)
	hours := kit.Between(date, 6+6, 6+6+5)
	minutes := kit.Between(date, 6, 6+6)
	seconds := kit.To(date, 6)
	b.AddMillis((((days*24+hours)*60+minutes)*60 + seconds) * 1000)
	pos.SetTime(b.Date())

	pos.Valid = true
	pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)

	flags := buf.ReadUnsignedIntLE()
	pos.Set(model.KeyIgnition, kit.Check(flags, 0))
	if kit.Check(flags, 1) {
		pos.AddAlarm(model.AlarmGeneral)
	}
	pos.Set(model.KeyInput, kit.Between(flags, 2, 7))
	pos.Set(model.KeyOutput, kit.Between(flags, 7, 10))
	pos.Course = float64(kit.Between(flags, 10, 13) * 45)
	pos.Set(model.KeyCharge, kit.Check(flags, 20))

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

	buf.ReadUnsignedByte() // input mask

	if kit.Check(infoGroups, 0) {
		buf.Skip(8) // waypoints
	}

	if kit.Check(infoGroups, 1) {
		buf.Skip(8) // wireless accessory
	}

	if kit.Check(infoGroups, 2) {
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Set(model.KeyHdop, buf.ReadUnsignedByte())
		pos.Accuracy = float64(buf.ReadUnsignedByte())
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		buf.ReadUnsignedShortLE() // time since boot
		pos.Set(model.KeyPower, buf.ReadUnsignedByte())
		pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))
	}

	if kit.Check(infoGroups, 3) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	}

	if kit.Check(infoGroups, 4) {
		pos.Set(model.KeyHours, units.MsFromMinutes(buf.ReadUnsignedIntLE()))
	}

	if kit.Check(infoGroups, 5) {
		buf.ReadUnsignedIntLE() // reason
	}

	if kit.Check(infoGroups, 6) {
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)
		pos.Set(model.KeyBattery, buf.ReadUnsignedShortLE())
	}

	if kit.Check(infoGroups, 7) {
		pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
	}

	buf.SetReaderIndex(buf.WriterIndex() - 3)
	sendResponse(m, device, id, buf.ReadUnsignedShortLE())

	return pos
}
