// Package thuraya is the Thuraya protocol (ThurayaProtocol): binary "#T" frames with a
// length field, acknowledged one by one.
package thuraya

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "thuraya",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, -4, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgEvent          = 0x5101
	msgPeriodicReport = 0x7101
	msgAck            = 0x9901
)

// checksum is the two's complement of the sum of the signed bytes.
func checksum(data []byte) int {
	crc := int32(0)
	for _, b := range data {
		crc += int32(int8(b))
	}
	return int(-crc)
}

func sendResponse(m *ingest.Message, id int64, typ int) {
	response := kit.Empty()
	response.WriteASCII("#T")
	response.WriteShort(15) // length
	response.WriteShort(msgAck)
	response.WriteInt(id)
	response.WriteShort(typ)
	response.WriteShort(1) // server ok
	response.WriteShort(checksum(response.Bytes()))
	m.Send(response)
}

func readString(buf *kit.Buf) string {
	end := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0)
	value := buf.ReadString(end - buf.ReaderIndex())
	buf.ReadUnsignedByte() // delimiter
	return value
}

func decodeLocation(buf *kit.Buf, pos *model.Position) {
	pos.Valid = true

	db := kit.NewDateBuilder(nil)
	date := int(buf.ReadInt())
	db.SetDay(date % 100)
	date /= 100
	db.SetMonth(date % 100)
	date /= 100
	db.SetYear(date)

	t := int(buf.ReadInt())
	db.SetSecond(t % 100)
	t /= 100
	db.SetMinute(t % 100)
	t /= 100
	db.SetHour(t)
	pos.SetTime(db.Date())

	pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
	pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)

	data := buf.ReadUnsignedShort()
	switch kit.FromInt(data, 12) {
	case 1:
		pos.Set(model.KeyIgnition, true)
	case 2:
		pos.Set(model.KeyIgnition, false)
	}
	pos.Course = float64(kit.ToInt(data, 12))
	pos.Speed = float64(buf.ReadShort())
	pos.Set(model.KeyRpm, buf.ReadShort())
	pos.Set("data", readString(buf))
}

func decodeAlarm(event int) string {
	switch event {
	case 10:
		return model.AlarmVibration
	case 11:
		return model.AlarmOverspeed
	case 12:
		return model.AlarmPowerCut
	case 13:
		return model.AlarmLowBattery
	case 18:
		return model.AlarmGpsAntennaCut
	case 20:
		return model.AlarmAcceleration
	case 21:
		return model.AlarmBraking
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // service
	buf.ReadUnsignedShort()
	typ := buf.ReadUnsignedShort()
	id := buf.ReadUnsignedInt()

	sendResponse(m, id, typ)

	ds := m.Session(strconv.FormatInt(id, 10))
	if ds == nil {
		return nil
	}

	switch typ {
	case msgEvent:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		decodeLocation(buf, pos)
		event := buf.ReadUnsignedByte()
		pos.AddAlarm(decodeAlarm(event))
		pos.Set(model.KeyEvent, event)
		pos.Set("eventData", readString(buf))
		return pos
	case msgPeriodicReport:
		positions := []*model.Position{}
		count := buf.ReadUnsignedByte()
		for range count {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			decodeLocation(buf, pos)
			positions = append(positions, pos)
		}
		return positions
	}
	return nil
}
