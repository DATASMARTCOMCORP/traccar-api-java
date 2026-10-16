// Package uux is the UUX protocol (UuxProtocol): binary frames with ASCII fields over TCP.
package uux

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "uux",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 1, 0, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgGeneral     = 0x90
	msgImmobilizer = 0x9E
	msgAck         = 0xD0
	msgNack        = 0xF0
	msgKeepalive   = 0xFF
)

func sendResponse(m *ingest.Message, productCode, protocolVersion, typ int) {
	if kit.Check(int64(protocolVersion), 7) {
		response := kit.Empty()
		response.WriteShort(productCode)
		response.WriteByte8(kit.ToInt(protocolVersion, 7))
		response.WriteByte8(1) // length
		response.WriteByte8(typ)
		m.Send(response)
	}
}

func readInt(buf *kit.Buf, length int) int { return kit.Int(buf.ReadString(length)) }

func readDouble(buf *kit.Buf, length int) float64 { return kit.Double(buf.ReadString(length)) }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	productCode := buf.ReadUnsignedShort()
	protocolVersion := buf.ReadUnsignedByte()
	buf.ReadUnsignedByte() // length
	typ := buf.ReadUnsignedByte()

	if typ == msgKeepalive {
		return nil
	}

	vehicleID := buf.ReadString(10)
	ds := m.Session(vehicleID)
	if ds == nil {
		sendResponse(m, productCode, protocolVersion, msgNack)
		return nil
	}

	b := kit.NewDateBuilder(nil)
	// Calendar.getInstance(): the year in the server's default time zone.
	b.SetDate(m.Now.In(m.Zone()).Year(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	b.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())

	switch typ {
	case msgGeneral:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(b.Date())

		buf.Skip(10)            // reason
		buf.ReadUnsignedShort() // flags

		buf.ReadUnsignedByte() // position status
		pos.Valid = true

		pos.Set(model.KeySatellites, readInt(buf, 2))

		latitude := float64(readInt(buf, 2))
		latitude += readDouble(buf, 7) / 60
		if buf.ReadUnsignedByte() == 'S' {
			latitude = -latitude
		}
		pos.SetLatitude(latitude)

		longitude := float64(readInt(buf, 3))
		longitude += readDouble(buf, 7) / 60
		if buf.ReadUnsignedByte() == 'W' {
			longitude = -longitude
		}
		pos.SetLongitude(longitude)

		pos.Speed = float64(readInt(buf, 3))
		pos.Course = float64(readInt(buf, 3))
		readInt(buf, 3) // alternative speed

		odometer := buf.ReadUnsignedByte()*10000 + buf.ReadUnsignedByte()*256 + buf.ReadUnsignedByte()
		pos.Set(model.KeyOdometer, float64(odometer)+float64(buf.ReadUnsignedByte())/10.0)
		pos.Set(model.KeyHours, buf.ReadUnsignedInt())
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())

		pos.Set("companyId", buf.ReadString(6))

		buf.Skip(10) // reason data

		pos.Set("tripId", buf.ReadUnsignedShort())
		return pos

	case msgImmobilizer:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		t := b.Date()
		m.LastLocation(pos, &t)

		pos.Set("companyId", buf.ReadString(6))
		pos.Set("tripId", buf.ReadUnsignedShort())
		return pos
	}

	sendResponse(m, productCode, protocolVersion, msgAck)
	return nil
}
