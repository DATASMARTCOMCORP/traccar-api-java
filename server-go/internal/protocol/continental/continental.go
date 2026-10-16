// Package continental is the Continental protocol (ContinentalProtocol): binary "SV"
// frames over TCP.
package continental

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
		Name: "continental",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, -4, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgKeepalive = 0x00
	msgStatus    = 0x02
	msgAck       = 0x06
	msgNack      = 0x15
)

func readCoordinate(buf *kit.Buf, extended bool) float64 {
	value := buf.ReadUnsignedInt()
	if extended {
		if value&0x08000000 != 0 {
			value |= 0xF0000000
		}
		return float64(int32(value)) / 360000.0
	}
	if value&0x00800000 != 0 {
		value |= 0xFF000000
	}
	return float64(int32(value)) / 3600.0
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2)             // header
	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedByte()  // software version

	ds := m.Session(strconv.FormatInt(buf.ReadUnsignedInt(), 10))
	if ds == nil {
		return nil
	}

	buf.ReadUnsignedByte() // product

	if buf.ReadUnsignedByte() != msgStatus {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.FixTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

	extended := buf.GetUnsignedByte(buf.ReaderIndex()) != 0
	pos.SetLatitude(readCoordinate(buf, extended))
	pos.SetLongitude(readCoordinate(buf, extended))

	pos.Course = float64(buf.ReadUnsignedShort())
	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))

	pos.Valid = buf.ReadUnsignedByte() > 0

	pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

	pos.Set(model.KeyEvent, buf.ReadUnsignedShort())

	input := buf.ReadUnsignedShort()
	pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))
	pos.Set(model.KeyInput, input)

	pos.Set(model.KeyOutput, buf.ReadUnsignedShort())
	pos.Set(model.KeyBattery, buf.ReadUnsignedByte())
	pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))

	buf.ReadUnsignedShort() // reserved

	if buf.ReadableBytes() > 4 {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
	}
	if buf.ReadableBytes() > 4 {
		pos.Set(model.KeyHours, units.MsFromHours(buf.ReadUnsignedInt()))
	}
	return pos
}
