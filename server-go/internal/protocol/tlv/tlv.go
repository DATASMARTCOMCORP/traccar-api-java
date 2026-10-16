// Package tlv is the TLV protocol (TlvProtocol): a two-character type followed by
// length-prefixed text arguments, frames ending with a zero byte.
package tlv

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
		Name: "tlv",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\x00"),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

func sendResponse(m *ingest.Message, typ string, arguments ...string) {
	response := kit.Empty()
	response.WriteASCII(typ)
	for _, argument := range arguments {
		response.WriteByte8(kit.Len(argument))
		response.WriteASCII(argument)
	}
	response.WriteByte8(0)
	m.Send(response)
}

func readArgument(buf *kit.Buf) string {
	return buf.ReadString(buf.ReadUnsignedByte())
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	typ := buf.ReadString(2)

	switch typ {
	case "0A", "0C":
		sendResponse(m, typ)
	case "0B":
		sendResponse(m, typ, "1482202689", "10", "20", "15")
	case "0E", "0F":
		sendResponse(m, typ, "30", "Unknown")
	}

	if typ != "0E" {
		return nil
	}

	ds := m.Session(readArgument(buf))
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Valid = true
	pos.SetTime(time.UnixMilli(kit.Long(readArgument(buf)) * 1000))

	readArgument(buf) // location identifier

	pos.SetLongitude(kit.Double(readArgument(buf)))
	pos.SetLatitude(kit.Double(readArgument(buf)))
	pos.Speed = units.KnotsFromKph(kit.Double(readArgument(buf)))
	pos.Course = kit.Double(readArgument(buf))

	pos.Set(model.KeySatellites, kit.Int(readArgument(buf)))

	return pos
}
