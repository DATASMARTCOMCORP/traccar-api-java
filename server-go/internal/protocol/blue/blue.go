// Package blue is the Blue protocol (BlueProtocol): binary frames of typed blocks over
// TCP.
package blue

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "blue",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 1, 2, -2, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

func readCoordinate(buf *kit.Buf, negative bool) float64 {
	value := buf.ReadUnsignedShort()
	degrees := value / 100
	minutes := float64(value%100) + float64(buf.ReadUnsignedShort())/10000.0
	coordinate := float64(degrees) + minutes/60
	if negative {
		return -coordinate
	}
	return coordinate
}

func sendResponse(m *ingest.Message, deviceIndex int) {
	response := kit.Empty()
	response.WriteByte8(0xaa)
	response.WriteShort(2 + 1 + 1 + 6 + 1)
	response.WriteByte8(0x86) // version
	response.WriteByte8(0)

	response.WriteByte8(6)    // data length
	response.WriteByte8(0xa4) // type
	response.WriteByte8(0)    // server index
	response.WriteByte8(deviceIndex)
	response.WriteByte8(0)
	response.WriteByte8(0)

	response.WriteByte8(kit.XOR(response.Bytes()[1:]))
	m.Send(response)
}

func decodeAlarm(value int) string {
	switch value {
	case 1:
		return model.AlarmSos
	case 8:
		return model.AlarmOverspeed
	case 19:
		return model.AlarmLowPower
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte()  // header
	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedByte()  // version
	buf.ReadUnsignedByte()

	ds := m.Session(strconv.FormatInt(buf.ReadUnsignedInt(), 10))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	for buf.ReadableBytes() > 1 {
		frameEnd := buf.ReaderIndex() + buf.ReadUnsignedByte()

		typ := buf.ReadUnsignedByte()
		index := buf.ReadUnsignedByte()
		buf.ReadUnsignedByte()
		buf.ReadUnsignedByte() // flags

		switch typ {
		case 0x01:
			buf.ReadUnsignedByte() // reserved
			flags := int64(buf.ReadUnsignedByte())

			pos.Valid = kit.Check(flags, 7)
			pos.SetLatitude(readCoordinate(buf, kit.Check(flags, 6)))
			pos.SetLongitude(readCoordinate(buf, kit.Check(flags, 5)))
			pos.Speed = float64(buf.ReadUnsignedShort()) + float64(buf.ReadUnsignedShort())/1000.0
			pos.Course = float64(buf.ReadUnsignedShort()) + float64(buf.ReadUnsignedByte())/100.0

			date := kit.NewDateBuilder(nil).
				SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
				SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			pos.SetTime(date.Date())

			buf.ReadUnsignedShort() // lac
			buf.ReadUnsignedShort() // cid

		case 0x12:
			status := int64(buf.ReadUnsignedByte()) // status 1
			if kit.Check(status, 1) {
				pos.AddAlarm(model.AlarmVibration)
			}

			buf.ReadUnsignedByte() // status 2
			buf.ReadUnsignedByte() // status 3

			status = int64(buf.ReadUnsignedByte()) // status 4
			ignition := kit.Between(status, 2, 4)
			if ignition == 0b01 {
				pos.Set(model.KeyIgnition, false)
			}
			if ignition == 0b10 {
				pos.Set(model.KeyIgnition, true)
			}

			buf.ReadUnsignedByte() // status 5
			buf.ReadUnsignedByte() // status 6

			pos.Set(model.KeyStatus, buf.ReadUnsignedShort())

		case 0x81:
			pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))

		case 0x84:
			sendResponse(m, index)
		}

		buf.SetReaderIndex(frameEnd)
	}

	if pos.FixTime.IsZero() {
		return nil
	}
	return pos
}
