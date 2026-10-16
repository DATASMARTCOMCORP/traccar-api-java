// Package blackkite is the BlackKite protocol (BlackKiteProtocol): Galileo-framed binary
// tagged records over TCP.
package blackkite

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/galileo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "blackkite",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: galileo.Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	tagIMEI           = 0x03
	tagDate           = 0x20
	tagCoordinates    = 0x30
	tagSpeedCourse    = 0x33
	tagAltitude       = 0x34
	tagStatus         = 0x40
	tagDigitalOutputs = 0x45
	tagDigitalInputs  = 0x46
	tagInputVoltage1  = 0x50
	tagInputVoltage2  = 0x51
	tagInputVoltage3  = 0x52
	tagInputVoltage4  = 0x53
	tagXT1            = 0x60
	tagXT2            = 0x61
	tagXT3            = 0x62
)

func sendResponse(m *ingest.Message, checksum int) {
	reply := kit.Empty()
	reply.WriteByte8(0x02)
	reply.WriteShortLE(int(int16(checksum)))
	m.Send(reply)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header
	length := (buf.ReadUnsignedShortLE() & 0x7fff) + 3

	var positions []*model.Position
	tags := map[int]bool{}
	hasLocation := false
	pos := m.NewPosition()

	for buf.ReaderIndex() < length {
		// Check if new message started
		tag := buf.ReadUnsignedByte()
		if tags[tag] {
			if hasLocation && !pos.FixTime.IsZero() {
				positions = append(positions, pos)
			}
			clear(tags)
			hasLocation = false
			pos = m.NewPosition()
		}
		tags[tag] = true

		switch tag {
		case tagIMEI:
			m.Session(buf.ReadString(15))
		case tagDate:
			pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		case tagCoordinates:
			hasLocation = true
			pos.Valid = buf.ReadUnsignedByte()&0xf0 == 0x00
			pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)
		case tagSpeedCourse:
			pos.Speed = float64(buf.ReadUnsignedShortLE()) * 0.0539957
			pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0
		case tagAltitude:
			pos.Altitude = float64(buf.ReadShortLE())
		case tagStatus:
			status := int64(buf.ReadUnsignedShortLE())
			pos.Set(model.KeyIgnition, kit.Check(status, 9))
			if kit.Check(status, 15) {
				pos.AddAlarm(model.AlarmGeneral)
			}
			pos.Set(model.KeyCharge, kit.Check(status, 2))
		case tagDigitalInputs:
			input := int64(buf.ReadUnsignedShortLE())
			for i := 0; i < 16; i++ {
				pos.Set(model.PrefixIo+strconv.Itoa(i+1), kit.Check(input, i))
			}
		case tagDigitalOutputs:
			output := int64(buf.ReadUnsignedShortLE())
			for i := 0; i < 16; i++ {
				pos.Set(model.PrefixIo+strconv.Itoa(i+17), kit.Check(output, i))
			}
		case tagInputVoltage1, tagInputVoltage2, tagInputVoltage3, tagInputVoltage4:
			pos.Set(model.PrefixAdc+strconv.Itoa(tag-tagInputVoltage1+1), float64(buf.ReadUnsignedShortLE())/1000.0)
		case tagXT1, tagXT2, tagXT3:
			buf.Skip(16)
		}
	}

	if hasLocation && !pos.FixTime.IsZero() {
		positions = append(positions, pos)
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	sendResponse(m, buf.ReadUnsignedShortLE())

	for _, p := range positions {
		p.DeviceID = ds.DeviceID
	}
	if len(positions) == 0 {
		return nil
	}
	return positions
}
