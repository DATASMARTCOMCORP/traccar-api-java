// Package t622iridium is the T622 Iridium protocol (T622IridiumProtocol): Iridium SBD
// messages over TCP whose payload fields are listed by the "format" setting.
package t622iridium

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
		Name: "t622iridium",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 1, 2, 0, 0),
				Decoder: &Decoder{},
			}
		}}},
	}
}

// Decoder is T622IridiumProtocolDecoder.
type Decoder struct {
	// Format is the default list of payload fields (hexadecimal ids separated by commas),
	// used when the device has no <protocol>.format attribute. Java leaves it null (a
	// NullPointerException) unless set.
	Format *string
}

func (d *Decoder) parameters(m *ingest.Message, deviceID int64) []int {
	value, err := m.LookupString(deviceID, m.Protocol()+".format", false, true)
	if err != nil {
		panic(err)
	}
	if value == nil {
		value = d.Format
	}
	if value == nil {
		kit.Throw("NullPointerException")
	}
	var result []int
	for _, s := range kit.Split(*value, ",") {
		result = append(result, kit.IntRadix(s, 16))
	}
	return result
}

// Decode decodes one message.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte()  // protocol revision
	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedByte()  // header indicator
	buf.ReadUnsignedShort() // header length
	buf.ReadUnsignedInt()   // reference

	imei := buf.ReadString(15)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	buf.ReadUnsignedByte()  // session status
	buf.ReadUnsignedShort() // originator index
	buf.ReadUnsignedShort() // transfer index
	buf.ReadUnsignedInt()   // session time
	buf.ReadUnsignedByte()  // payload indicator
	buf.ReadUnsignedShort() // payload length

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	for _, parameter := range d.parameters(m, ds.DeviceID) {
		switch parameter {
		case 0x01:
			pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
		case 0x02:
			pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
		case 0x03:
			pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)
		case 0x04:
			pos.SetTime(time.UnixMilli((buf.ReadUnsignedIntLE() + 946684800) * 1000))
		case 0x05:
			pos.Valid = buf.ReadUnsignedByte() > 0
		case 0x06:
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		case 0x07:
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		case 0x08:
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
		case 0x09:
			pos.Course = float64(buf.ReadUnsignedShortLE())
		case 0x0A:
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
		case 0x0B:
			pos.Altitude = float64(buf.ReadShortLE())
		case 0x0C:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
		case 0x0D:
			pos.Set(model.KeyHours, buf.ReadUnsignedIntLE()*1000)
		case 0x14:
			pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
		case 0x15:
			pos.Set(model.KeyInput, buf.ReadUnsignedByte())
		case 0x19:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/100.0)
		case 0x1A:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/100.0)
		case 0x1B:
			buf.ReadUnsignedByte() // geofence
		}
	}

	return pos
}
