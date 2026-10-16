// Package ndtpv6 is the NDTP v6 protocol (NdtpV6Protocol): binary NPL packets carrying
// NPH services (connection requests, navigation data).
package ndtpv6

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server without a frame decoder. As in
// Java, the command encoder (NdtpV6ProtocolEncoder) isn't in the pipeline.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "ndtpv6",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var signature = []byte{0x7E, 0x7E}

const (
	nplFlagCRC         = 2
	nphResultOK        = 0x00000000
	nplTypeNPH         = 2
	nplAddressServer   = 0
	nphResult          = 0
	nphSrvGenericCtrls = 0
	nphSrvNavdata      = 1
	nphSgcResult       = nphResult
	nphSgcConnRequest  = 100
	nphSndResult       = nphResult
	mainNavData        = 0
	additionalNavData  = 2
)

func sendResponse(m *ingest.Message, serviceID int, requestID int64) {
	content := kit.Empty()
	content.WriteShortLE(serviceID)
	content.WriteIntLE(nphSndResult)
	content.WriteIntLE(requestID)
	content.WriteIntLE(nphResultOK)

	response := kit.Empty()
	response.WriteBytes(signature)
	response.WriteShortLE(content.ReadableBytes())
	response.WriteShortLE(nplFlagCRC) // flags
	response.WriteShort(kit.CRC16(kit.CRC16_MODBUS, content.Bytes()))
	response.WriteByte8(nplTypeNPH)       // type
	response.WriteIntLE(nplAddressServer) // peer address
	response.WriteShortLE(0)              // request id
	response.WriteBuf(content)

	m.Send(response)
}

func decodeData(buf *kit.Buf, pos *model.Position) {
	itemType := buf.ReadUnsignedByte()
	itemIndex := buf.ReadUnsignedByte()
	if itemType == mainNavData && (itemIndex == 0 || itemIndex == 1) {
		pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)

		flags := int64(buf.ReadUnsignedByte())
		pos.Valid = kit.Check(flags, 7)
		if kit.Check(flags, 1) {
			pos.AddAlarm(model.AlarmGeneral)
		}

		pos.Set(model.KeyBattery, buf.ReadUnsignedByte()*20)
		pos.Set(model.KeyObdSpeed, buf.ReadUnsignedShortLE())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
		pos.Course = float64(buf.ReadUnsignedShortLE())

		pos.Set(model.KeyOdometer, buf.ReadUnsignedShortLE())
		pos.Altitude = float64(buf.ReadShortLE())
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Set(model.KeyPdop, buf.ReadUnsignedByte())
	}

	itemType = buf.ReadUnsignedByte()
	itemIndex = buf.ReadUnsignedByte()
	if itemType == additionalNavData && itemIndex == 0 {
		pos.Set(model.KeyBatteryLevel, max((buf.ReadUnsignedShortLE()-3600)/6, 100))
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShortLE())
		pos.Set(model.PrefixAdc+"3", buf.ReadUnsignedShortLE())
		pos.Set(model.PrefixAdc+"4", buf.ReadUnsignedShortLE())

		buf.ReadUnsignedByte()    // inputs
		buf.ReadUnsignedByte()    // outputs
		buf.ReadUnsignedShortLE() // in1 count
		buf.ReadUnsignedShortLE() // in2 count
		buf.ReadUnsignedShortLE() // in3 count
		buf.ReadUnsignedShortLE() // in4 count
		buf.ReadUnsignedIntLE()   // track length

		pos.Set(model.KeyAntenna, buf.ReadUnsignedByte())
		pos.Set(model.KeyGps, buf.ReadUnsignedByte())
		pos.Set(model.KeyAcceleration, buf.ReadUnsignedByte())
		pos.Set(model.KeyPower, buf.ReadUnsignedByte()*200)
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	ds := m.Session()

	buf.Skip(2)               // signature
	buf.ReadUnsignedShortLE() // length
	buf.ReadUnsignedShortLE() // connection flags
	buf.ReadUnsignedShortLE() // checksum
	buf.ReadUnsignedByte()    // type
	buf.ReadUnsignedIntLE()   // address
	buf.ReadUnsignedShortLE() // identification

	serviceID := buf.ReadUnsignedShortLE()
	serviceType := buf.ReadUnsignedShortLE()
	buf.ReadUnsignedShortLE() // request flags
	requestID := buf.ReadUnsignedIntLE()

	if ds == nil && serviceID == nphSrvGenericCtrls && serviceType == nphSgcConnRequest {
		buf.ReadUnsignedShortLE() // version major
		buf.ReadUnsignedShortLE() // version minor
		buf.ReadUnsignedShortLE() // connection flags

		deviceID := buf.ReadUnsignedShortLE()
		pos := m.NewPosition()
		ds = m.Session(strconv.Itoa(deviceID))
		pos.DeviceID = ds.DeviceID // a null session throws, as in Java

		sendResponse(m, serviceID, requestID)

		pos.Set(model.KeyResult, strconv.Itoa(nphSgcResult))
		pos.SetTime(m.Now)
		now := m.Now
		m.LastLocation(pos, &now)
		pos.Valid = false

		return pos
	}

	if serviceID == nphSrvNavdata {
		ds = m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		sendResponse(m, serviceID, requestID)

		decodeData(buf, pos)

		return pos
	}

	return nil
}

// encode is NdtpV6ProtocolEncoder (which the Java protocol doesn't install).
func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandIdentification:
		return "BB+IDNT"
	case model.CommandRebootDevice:
		return "BB+RESET"
	case model.CommandPositionSingle:
		return "BB+RRCD"
	}
	return nil
}
