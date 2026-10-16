// Package anytrek is the Anytrek protocol (AnytrekProtocol): little-endian binary 0x7878
// frames over TCP, each acknowledged.
package anytrek

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "anytrek",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, 2, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func sendResponse(m *ingest.Message, typ int) {
	response := kit.Empty()
	response.WriteShort(0x7878)
	response.WriteShortLE(1 + 1 + 2 + 1 + 2) // length
	response.WriteByte8(typ)
	response.WriteByte8(0)    // error
	response.WriteShortLE(0)  // report interval
	response.WriteByte8(0)    // clear alarm
	response.WriteShortLE(0)  // checksum
	response.WriteByte8('\r')
	response.WriteByte8('\n')
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2)                // header
	buf.ReadUnsignedShortLE() // size
	typ := buf.ReadUnsignedByte()

	imei := buf.ReadSlice(8).HexDump()[2:]
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyVersionFw, buf.ReadUnsignedShortLE())
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/100.0)
	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())

	date := kit.NewDateBuilder(nil).
		SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	pos.SetTime(date.Date())

	pos.Set(model.KeySatellites, kit.ToInt(buf.ReadUnsignedByte(), 4))

	latitude := float64(buf.ReadUnsignedIntLE()) / 1800000.0
	longitude := float64(buf.ReadUnsignedIntLE()) / 1800000.0
	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

	flags := buf.ReadUnsignedShortLE()
	pos.Course = float64(kit.ToInt(flags, 10))
	pos.Valid = kit.Check(int64(flags), 12)
	if !kit.Check(int64(flags), 10) {
		latitude = -latitude
	}
	if kit.Check(int64(flags), 11) {
		longitude = -longitude
	}
	pos.SetLatitude(latitude)
	pos.SetLongitude(longitude)

	buf.ReadUnsignedIntLE() // info index
	buf.ReadUnsignedIntLE() // setting index

	flags = buf.ReadUnsignedByte()
	pos.Set(model.KeyCharge, kit.Check(int64(flags), 0))
	pos.Set(model.KeyIgnition, kit.Check(int64(flags), 1))
	if kit.Check(int64(flags), 4) {
		pos.AddAlarm(model.AlarmGeneral)
	}

	buf.ReadUnsignedShortLE() // charge current

	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

	sendResponse(m, typ)
	return pos
}
