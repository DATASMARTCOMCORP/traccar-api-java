// Package positrex is the Positrex protocol (PositrexProtocol): binary UDP ping reports.
package positrex

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "positrex",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const msgPing = 0x2E

// readTime reads a packed date: seconds, minutes, hours, days (of 32) and months (of 13)
// since 2000.
func readTime(buf *kit.Buf) time.Time {
	t := buf.ReadUnsignedInt()
	date := kit.NewDateBuilder(nil)
	date.SetSecond(int(t % 60))
	t /= 60
	date.SetMinute(int(t % 60))
	t /= 60
	date.SetHour(int(t % 24))
	t /= 24
	date.SetDay(int(t % 32))
	t /= 32
	date.SetMonth(int(t % 13))
	date.SetYear(int(2000 + t/13))
	return date.Date()
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	first := int64(buf.GetUnsignedByte(buf.ReaderIndex()))
	var deviceID int64
	switch {
	case kit.Check(first, 7) && kit.Check(first, 6):
		deviceID = 73000000 + kit.To(buf.ReadUnsignedInt(), 30)
	case kit.Check(first, 7) && !kit.Check(first, 5) && !kit.Check(first, 4):
		deviceID = 7590000 + int64(kit.ToInt(buf.ReadUnsignedMedium(), 20))
	case kit.Check(first, 7):
		deviceID = 70000000 + int64(kit.ToInt(buf.ReadUnsignedMedium(), 20))
	default:
		deviceID = 7560000 + int64(buf.ReadUnsignedShort())
	}

	ds := m.Session(strconv.FormatInt(deviceID, 10))
	if ds == nil {
		return nil
	}

	if buf.ReadUnsignedByte() != msgPing {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(readTime(buf))

	latitude := buf.ReadMedium()
	longitude := buf.ReadMedium()

	pos.Valid = kit.Check(int64(latitude), 23)
	pos.SetLatitude(float64(kit.ToInt(latitude, 23)) / 40000.0)
	pos.SetLongitude(float64(longitude) / 40000.0)

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	pos.Course = float64(buf.ReadUnsignedByte() * 2)

	pos.Set(model.PrefixIo, buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
	buf.ReadUnsignedInt() // report begin
	buf.ReadUnsignedInt() // report end
	buf.ReadUnsignedInt() // number of records

	if buf.IsReadable() {
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
	}

	return pos
}
