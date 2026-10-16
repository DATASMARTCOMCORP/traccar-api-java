// Package obddongle is the OBD dongle protocol (ObdDongleProtocol): MQTT-like binary
// messages over TCP.
package obddongle

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "obddongle",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLengthLarge, 20, 2, 3, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgConnect = 0x01
	msgConnAck = 0x02
	msgPublish = 0x03
	msgPubAck  = 0x04
)

func sendResponse(m *ingest.Message, typ, index int, imei string, content []byte) {
	response := kit.Empty()
	response.WriteShort(0x5555) // header
	response.WriteShort(index)
	response.WriteBytes(kit.ASCIIBytes(imei))
	response.WriteByte8(typ)
	response.WriteShort(len(content))
	response.WriteBytes(content)
	response.WriteByte8(0) // checksum
	response.WriteShort(0xAAAA)
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2) // header
	index := buf.ReadUnsignedShort()
	imei := buf.ReadString(15)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // data length

	switch typ {
	case msgConnect:
		sendResponse(m, msgConnAck, index, imei, []byte{1, 0, 0, 0, 0, 0, 0})
	case msgPublish:
		typeMajor := buf.ReadUnsignedByte()
		typeMinor := buf.ReadUnsignedByte()
		buf.ReadUnsignedByte() // event id

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		flags := buf.ReadUnsignedByte()
		pos.Valid = !kit.Check(int64(flags), 6)
		pos.Set(model.KeySatellites, kit.ToInt(flags, 4))

		longitude := float64(kit.ToInt(buf.ReadUnsignedShort(), 1)<<24+buf.ReadUnsignedMedium()) / 100000.0
		if !kit.Check(int64(flags), 5) {
			longitude = -longitude
		}
		pos.SetLongitude(longitude)
		latitude := float64(buf.ReadUnsignedMedium()) / 100000.0
		if !kit.Check(int64(flags), 4) {
			latitude = -latitude
		}
		pos.SetLatitude(latitude)

		speedCourse := buf.ReadUnsignedMedium()
		pos.Speed = units.KnotsFromMph(float64(kit.FromInt(speedCourse, 10)) / 10.0)
		pos.Course = float64(kit.ToInt(speedCourse, 10))

		sendResponse(m, msgPubAck, index, imei, []byte{byte(typeMajor), byte(typeMinor)})
		return pos
	}
	return nil
}
