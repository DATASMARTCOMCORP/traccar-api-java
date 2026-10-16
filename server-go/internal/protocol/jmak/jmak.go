// Package jmak is the Jmak protocol (JmakProtocol): '~...$' masked text events, with
// JSON and '^...$' messages acknowledged only, over TCP.
package jmak

import (
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "jmak",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is JmakFrameDecoder: JSON objects, or '~'/'^' frames ending with '$'.
func Framer() ingest.Framer {
	json := ingest.JSONFramer()
	return &ingest.BaseFramer{Decode: func(m *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() == 0 {
			return nil
		}
		first := buf.GetByte(buf.ReaderIndex())
		if first == '{' {
			return json.Frame(m, buf)
		}
		if first == '~' || first == '^' {
			delimiterIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '$')
			if delimiterIndex > 0 {
				return buf.ReadSlice(delimiterIndex - buf.ReaderIndex() + 1)
			}
		}
		return nil
	}}
}

func ack(m *ingest.Message) { m.Send(kit.NewBuf([]byte("ACK"))) }

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	if strings.HasPrefix(sentence, "{") || strings.HasPrefix(sentence, "^") {
		ack(m)
		return nil
	}
	if !strings.HasPrefix(sentence, "~") || !strings.HasSuffix(sentence, "$") {
		return nil
	}

	parts := kit.Split(kit.Substring(sentence, 1, kit.Len(sentence)-1), "|")

	values := kit.Split(parts[0], ";")
	ds := m.Session(values[2])
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	decodeEvent(pos, values)

	if len(parts) >= 2 {
		decodeCan(pos, kit.Split(parts[1], ";"))
	}

	ack(m)
	return pos
}

// fields reads values in order; reading past the end panics, as Java's array access.
type fields struct {
	values []string
	i      int
}

func (f *fields) next() string {
	v := f.values[f.i]
	f.i++
	return v
}

func decodeEvent(pos *model.Position, values []string) {
	f := &fields{values: values}
	mask := kit.LongRadix(f.next(), 16)
	c := func(i int) bool { return kit.Check(mask, i) }

	if c(0) {
		f.i++ // serial number
	}
	if c(1) {
		f.i++ // imei
	}
	if c(2) {
		pos.Set(model.PrefixCount, kit.Long(f.next()))
	}
	if c(3) {
		if v := f.next(); v != "NULL" {
			pos.Set("nickname", v)
		}
	}
	if c(4) {
		pos.SetTime(time.UnixMilli(kit.Long(f.next())))
	}
	if c(5) {
		pos.SetLatitude(kit.Double(f.next()))
	}
	if c(6) {
		pos.SetLongitude(kit.Double(f.next()))
	}
	if c(7) {
		pos.Altitude = kit.Double(f.next())
	}
	if c(8) {
		f.i++
	}
	if c(9) {
		pos.Speed = units.KnotsFromKph(kit.Double(f.next()))
	}
	if c(10) {
		pos.Set(model.KeySatellites, kit.Int(f.next()))
	}
	if c(11) {
		pos.Set(model.KeyRssi, kit.Int(f.next()))
	}
	if c(12) {
		pos.Set(model.KeyHdop, kit.Double(f.next()))
	}
	if c(13) {
		pos.Course = kit.Double(f.next())
	}
	if c(14) {
		pos.Set(model.KeyIgnition, kit.Int(f.next()) == 1)
	}
	if c(15) {
		pos.Set("backup", kit.Int(f.next()) == 1)
	}
	if c(16) {
		pos.Set(model.KeyHours, kit.Double(f.next())*3600000)
	}
	if c(17) {
		pos.Set(model.KeyOdometer, kit.Double(f.next())*1000)
	}

	eventID := 0
	if c(18) {
		eventID = kit.Int(f.next())
		pos.Set(model.KeyEvent, eventID)
	}
	eventStatus := 0
	if c(19) {
		eventStatus = kit.Int(f.next())
		pos.Set("eventStatus", eventStatus)
	}
	var eventName *string
	if c(20) {
		v := f.next()
		eventName = &v
		pos.Set("eventName", v)
	}
	if eventID == 126 && eventStatus == 4 && eventName != nil {
		pos.Set(model.KeyDriverUniqueID, *eventName)
	}

	if c(21) {
		pos.Set(model.KeyVin, kit.Int(f.next()))
	}
	if c(22) {
		pos.Set(model.KeyBattery, kit.Int(f.next()))
	}
	if c(23) {
		pos.Set(model.KeyOperator, f.next())
	}
	if c(24) {
		f.i++ // cellular technology
	}
	if c(25) {
		pos.DeviceTime = model.NewTime(time.UnixMilli(kit.Long(f.next())))
	}
	if c(26) {
		pos.Valid = kit.Int(f.next()) >= 1
	}
	if c(27) {
		io := int64(kit.Int(f.next()))
		pos.Set(model.PrefixIn+"1", kit.Check(io, 0))
		pos.Set(model.PrefixIn+"2", kit.Check(io, 1))
		pos.Set(model.PrefixOut+"1", kit.Check(io, 2))
		pos.Set(model.PrefixOut+"2", kit.Check(io, 3))
	}
	if c(28) {
		pos.Set(model.KeyDriverUniqueID, f.next())
	}
	if c(29) {
		f.i++ // transparent message length
	}
	if c(30) {
		pos.Set("message", f.next())
	}
}

func decodeCan(pos *model.Position, values []string) {
	f := &fields{values: values}
	mask := kit.LongRadix(f.next(), 16)
	c := func(i int) bool { return kit.Check(mask, i) }

	if c(0) {
		pos.Set(model.KeyObdOdometer, kit.Double(f.next())*1000)
	}
	if c(1) {
		pos.Set(model.KeyHours, kit.Double(f.next())*3600000)
	}
	if c(2) {
		pos.Set(model.KeyObdSpeed, kit.Double(f.next()))
	}
	if c(3) {
		pos.Set(model.KeyRpm, kit.Double(f.next()))
	}
	if c(4) {
		pos.Set("canStatus", kit.Int(f.next()))
	}
	if c(5) {
		f.i++
	}
	if c(6) {
		pos.Set(model.KeyThrottle, kit.Double(f.next()))
	}
	if c(7) {
		f.i++
	}
	if c(8) {
		pos.Set(model.KeyFuel, kit.Double(f.next()))
	}
	if c(9) {
		f.i++
	}
	if c(10) {
		pos.Set("autonomy", kit.Double(f.next()))
	}
	if c(11) {
		f.i++
	}
	if c(12) {
		pos.Set(model.KeyFuelConsumption, kit.Double(f.next()))
	}
	if c(13) {
		pos.Set(model.KeyFuelUsed, kit.Double(f.next()))
	}
	if c(14) {
		pos.Set("oilTemperature", kit.Double(f.next()))
	}
}
