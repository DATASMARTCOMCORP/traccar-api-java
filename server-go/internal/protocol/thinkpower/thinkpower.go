// Package thinkpower is the ThinkPower protocol (ThinkPowerProtocol): binary
// type-length-value records over TCP.
package thinkpower

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
		Name: "thinkpower",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, 2, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgLoginRequest      = 0x01
	msgLoginResponse     = 0x02
	msgHeartbeatRequest  = 0x03
	msgHeartbeatResponse = 0x04
	msgRecordReport      = 0x05
	msgRecordResponse    = 0x06
)

func sendResponse(m *ingest.Message, typ, index int, content []byte) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteByte8(typ)
	response.WriteByte8(index)
	response.WriteShort(len(content))
	response.WriteBytes(content)
	response.WriteShort(kit.CRC16(kit.CRC16_CCITT_FALSE, response.Bytes()))
	m.Send(response)
}

func decodeValue(pos *model.Position, typ int, buf *kit.Buf) {
	switch typ {
	case 0x01:
		pos.Valid = true
		pos.SetLatitude(float64(kit.ReadSignedMagnitudeInt(buf)) / 10000000.0)
		pos.SetLongitude(float64(kit.ReadSignedMagnitudeInt(buf)) / 10000000.0)
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)
		pos.Course = float64(buf.ReadUnsignedShort()) / 100.0
	case 0x02:
		pos.Valid = buf.ReadUnsignedByte() > 0
	case 0x03:
		buf.Skip(3) // geofence
	case 0x06, 0x07, 0x08:
		buf.Skip(2) // g-sensor x/y/z
	case 0x09:
		buf.ReadUnsignedByte() // collision alarm
	case 0x0A:
		buf.ReadUnsignedByte() // drop alarm
	case 0x10:
		if buf.ReadUnsignedByte() > 0 {
			pos.AddAlarm(model.AlarmSos)
		}
	case 0x12:
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/10.0)
	case 0x13:
		if buf.ReadUnsignedByte() > 0 {
			pos.AddAlarm(model.AlarmLowBattery)
		}
	case 0x16:
		buf.ReadUnsignedShort() // temperature
	case 0x17:
		buf.ReadUnsignedByte() // humidity
	case 0x18:
		buf.ReadUnsignedShort() // high temperature
	case 0x19:
		buf.ReadUnsignedByte() // high humidity
	case 0x50:
		if buf.ReadUnsignedByte() > 0 {
			pos.AddAlarm(model.AlarmRemoving)
		}
	case 0x51:
		if buf.ReadUnsignedByte() > 0 {
			pos.AddAlarm(model.AlarmTampering)
		}
	}
}

// decode is ThinkPowerProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	typ := buf.ReadUnsignedByte()
	index := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length

	switch typ {
	case msgLoginRequest:

		buf.ReadUnsignedByte() // protocol major version
		buf.ReadUnsignedByte() // protocol minor version

		id := buf.ReadString(buf.ReadUnsignedByte())
		ds := m.Session(id)

		result := 4
		if ds != nil {
			result = 0
		}
		sendResponse(m, msgLoginResponse, index, []byte{byte(result)})

	case msgHeartbeatRequest:

		sendResponse(m, msgHeartbeatResponse, index, nil)

	case msgRecordReport:

		ds := m.Session()
		if ds == nil {
			return nil
		}

		buf.ReadUnsignedByte() // count

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

		for buf.ReadableBytes() > 2 {
			decodeValue(pos, buf.ReadUnsignedByte(), buf)
		}

		sendResponse(m, msgRecordResponse, index, nil)

		return pos

	}

	return nil
}
