// Package flexiblereport is the FlexibleReport protocol (FlexibleReportProtocol): binary
// UDP reports whose fields are selected by a mask.
package flexiblereport

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "flexiblereport",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const msgGeneral = 0x00

func sendResponse(m *ingest.Message, index int) {
	response := kit.Empty()
	response.WriteByte8(0x7E) // header
	response.WriteShort(2)    // length
	response.WriteByte8(0xE0)
	if kit.Check(int64(index), 0) {
		response.WriteByte8(0x4F)
	} else {
		response.WriteByte8(0x0F)
	}
	m.Send(response)
}

// decodeTime reads seconds in a calendar of 31-day months (Java int arithmetic).
func decodeTime(buf *kit.Buf) time.Time {
	t := buf.ReadInt()
	return kit.NewDateBuilder(nil).
		SetSecond(int(t % 60)).
		SetMinute(int((t / 60) % 60)).
		SetHour(int((t / (60 * 60)) % 24)).
		SetDay(int(1 + t/(60*60*24)%31)).
		SetMonth(int(1 + t/(60*60*24*31)%12)).
		SetYear(int(2000 + t/(60*60*24*31*12))).
		Date()
}

func check(v int64, i int) bool { return kit.Check(v, i) }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header
	flags := buf.ReadUnsignedByte()

	imei := kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	index := buf.ReadUnsignedShort()
	if kit.ToInt(flags, 2) > 0 {
		sendResponse(m, index)
	}

	deviceTime := decodeTime(buf)
	event := buf.ReadUnsignedByte()

	buf.ReadUnsignedByte() // length

	if buf.ReadUnsignedByte() != msgGeneral {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.DeviceTime = model.NewTime(deviceTime)
	pos.Set(model.KeyEvent, event)

	buf.ReadUnsignedByte() // length
	mask := buf.ReadUnsignedInt()

	if check(mask, 0) {
		buf.ReadUnsignedByte() // product id
	}
	if check(mask, 1) {
		pos.FixTime = model.NewTime(decodeTime(buf))
	}
	if check(mask, 2) {
		pos.Valid = true
		pos.SetLatitude(float64(buf.ReadUnsignedInt())/1000000.0 - 90)
		pos.SetLongitude(float64(buf.ReadUnsignedInt())/1000000.0 - 180)
	}
	if check(mask, 3) {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Course = float64(buf.ReadUnsignedShort())
	}
	if check(mask, 4) {
		pos.Altitude = float64(buf.ReadShort())
	}
	if check(mask, 5) {
		buf.ReadUnsignedShort() // gps accuracy
	}
	if check(mask, 6) {
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
	}
	if check(mask, 7) {
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
	}
	if check(mask, 8) {
		pos.Set("auxPower", float64(buf.ReadUnsignedShort())/1000.0)
	}
	if check(mask, 9) {
		pos.Set("solarPower", float64(buf.ReadUnsignedShort())/1000.0)
	}
	if check(mask, 10) {
		cellService := buf.ReadUnsignedByte()
		pos.Set(model.KeyRoaming, kit.Check(int64(cellService), 7))
		pos.Set("service", kit.ToInt(cellService, 7))
		buf.Skip(4) // cell info
	}
	if check(mask, 11) {
		buf.ReadUnsignedByte() // rssi
	}
	if check(mask, 12) {
		inputs := buf.ReadUnsignedByte()
		pos.Set(model.KeyIgnition, kit.Check(int64(inputs), 0))
		pos.Set(model.PrefixIo+"1", inputs)
	}
	if check(mask, 13) {
		pos.Set(model.PrefixIo+"2", buf.ReadUnsignedByte())
	}
	if check(mask, 14) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)
	}
	if check(mask, 15) {
		pos.Set(model.PrefixTemp+"1", float64(buf.ReadUnsignedShort())/100.0)
	}

	return pos
}
