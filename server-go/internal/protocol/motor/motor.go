// Package motor is the Motor protocol (MotorProtocol): hex-encoded binary records, one
// per line.
package motor

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "motor",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := kit.Hex(msg.(string))

	id := kit.Format("%08x", buf.ReadUnsignedIntLE())
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	buf.Skip(2) // divider

	pos.Set(model.KeyStatus, buf.ReadUnsignedShortLE())

	buf.Skip(2)                // divider
	buf.ReadUnsignedMediumLE() // command

	flags := buf.ReadUnsignedByte()
	pos.Valid = kit.Check(int64(flags), 7)
	if kit.Check(int64(flags), 0) {
		pos.AddAlarm(model.AlarmGeneral)
	}

	bcd := func(digits int) int { return kit.ReadBCD(buf, digits) }
	pos.SetLatitude(float64(bcd(2)) + float64(bcd(6))/10000.0/60)
	pos.SetLongitude(float64(bcd(4)) + float64(bcd(6))/10000.0/60)
	pos.Speed = float64(bcd(4)) / 10.0
	pos.Course = float64(bcd(4)) / 10.0

	b := kit.NewDateBuilder(nil)
	b.SetYear(bcd(2))
	b.SetMonth(bcd(2))
	b.SetDay(bcd(2))
	b.SetHour(bcd(2))
	b.SetMinute(bcd(2))
	b.SetSecond(bcd(2))
	pos.SetTime(b.Date())

	pos.Set(model.KeyRssi, bcd(2))

	return pos
}
