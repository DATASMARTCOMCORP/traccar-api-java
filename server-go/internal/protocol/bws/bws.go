// Package bws is the BWS protocol (BwsProtocol): binary reports over UDP, acknowledged
// with a CRC-8 reply.
package bws

import (
	"encoding/hex"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "bws",
		DataCommands: protocol.DataCommands("bws"),
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

const (
	msgKeepAlive = 0x3f
	msgAck       = 0x40
	msgNack      = 0x41
	msgAction    = 0x42
	msgCheck     = 0x43
	msgDefine    = 0x44
)

func sendResponse(m *ingest.Message, deviceType int, id []byte, typ, index int) {
	response := kit.Empty()
	response.WriteByte8(deviceType)
	response.WriteBytes(id)
	response.WriteByte8(typ)
	response.WriteByte8(index)
	response.WriteByte8(kit.CRC8(kit.CRC8_DALLAS, response.Bytes()))
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	deviceType := buf.ReadUnsignedByte()
	id := buf.ReadBytes(4)
	messageType := buf.ReadUnsignedByte()
	index := buf.ReadUnsignedByte()

	ds := m.Session(hex.EncodeToString(id))
	if ds != nil {
		sendResponse(m, deviceType, id, msgAck, index)
	} else {
		sendResponse(m, deviceType, id, msgNack, index)
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyEvent, messageType)

	pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
	pos.SetLatitude(float64(buf.ReadInt()) / 360000.0)
	pos.SetLongitude(float64(buf.ReadInt()) / 360000.0)

	flags := buf.ReadUnsignedByte()
	pos.Valid = kit.Check(int64(flags), 7)
	pos.Set(model.KeySatellites, kit.ToInt(flags, 7))

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	pos.Course = float64(buf.ReadUnsignedShort()) / 10.0

	pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/10.0)
	pos.Set(model.PrefixIo+"1", buf.ReadUnsignedShort())
	pos.Set(model.KeyOdometer, buf.ReadUnsignedMedium()*100)
	pos.Set(model.KeyHours, int64(buf.ReadUnsignedMedium())*60000)
	pos.Set(model.KeyDeviceTemp, buf.ReadUnsignedByte())
	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())

	buf.ReadUnsignedByte() // checksum

	return pos
}

func encodeContent(m *ingest.Message, deviceID int64, value int) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0x00)
	buf.WriteBytes(kit.ParseHex(kit.Substring(m.UniqueID(deviceID), 0, 8)))
	buf.WriteByte8(msgAction)
	buf.WriteByte8(0)
	buf.WriteByte8(value)
	buf.WriteByte8(kit.CRC8(kit.CRC8_DALLAS, buf.Bytes()))
	return buf
}

func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return encodeContent(m, c.DeviceID, 0)
	case model.CommandEngineResume:
		return encodeContent(m, c.DeviceID, 1)
	}
	return nil
}
