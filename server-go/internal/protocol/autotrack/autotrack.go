// Package autotrack is the AutoTrack protocol (AutoTrackProtocol): little-endian binary
// messages with a 0xF1F1F1F1 sync over TCP; logins and telemetry are confirmed.
package autotrack

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "autotrack",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 5, 2, 2, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgLoginRequest     = 51
	msgLoginConfirm     = 101
	msgTelemetry1       = 52
	msgTelemetry2       = 66
	msgTelemetry3       = 67
	msgTelemetryConfirm = 123
)

func sendWithChecksum(m *ingest.Message, response *kit.Buf) {
	response.WriteShort(kit.CRC16(kit.CRC16_XMODEM, response.Bytes()))
	m.Send(response)
}

func decodeTelemetry(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(time.UnixMilli(1009843200000 + buf.ReadUnsignedIntLE()*1000)) // seconds since 2002
	pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)

	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyFuelUsed, buf.ReadUnsignedIntLE())

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
	buf.ReadUnsignedShortLE() // max speed

	pos.Set(model.KeyInput, buf.ReadUnsignedShortLE())
	buf.ReadUnsignedIntLE() // di 3 count
	buf.ReadUnsignedIntLE() // di 4 count

	for i := 0; i < 5; i++ {
		pos.Set(model.PrefixAdc+strconv.Itoa(i+1), buf.ReadUnsignedShortLE())
	}

	pos.Course = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeyStatus, buf.ReadUnsignedShortLE())
	pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())
	pos.Set(model.KeyDriverUniqueID, buf.ReadLongLE())

	index := buf.ReadUnsignedShortLE()

	buf.ReadUnsignedShortLE() // checksum

	if m.HasChannel() {
		response := kit.Empty()
		response.WriteInt(0xF1F1F1F1) // sync
		response.WriteByte8(msgTelemetryConfirm)
		response.WriteShortLE(2) // length
		response.WriteShortLE(index)
		sendWithChecksum(m, response)
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(4) // sync
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShortLE() // length

	switch typ {
	case msgLoginRequest:
		imei := buf.ReadSlice(8).HexDump()
		if m.Session(imei) == nil {
			return nil
		}
		fuelConst := buf.ReadUnsignedShortLE()
		tripConst := buf.ReadUnsignedShortLE()
		if m.HasChannel() {
			response := kit.Empty()
			response.WriteInt(0xF1F1F1F1) // sync
			response.WriteByte8(msgLoginConfirm)
			response.WriteShortLE(12) // length
			response.WriteBytes(kit.ParseHex(imei))
			response.WriteShortLE(fuelConst)
			response.WriteShortLE(tripConst)
			sendWithChecksum(m, response)
		}
		return nil
	case msgTelemetry1, msgTelemetry2, msgTelemetry3:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		return decodeTelemetry(m, ds, buf)
	}
	return nil
}
