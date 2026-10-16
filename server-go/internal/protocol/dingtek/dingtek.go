// Package dingtek is the Dingtek protocol (DingtekProtocol): hex-encoded binary reports
// sent as text over TCP.
package dingtek

import (
	"encoding/hex"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "dingtek",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is DingtekFrameDecoder: the hex length at (absolute) index 8 counts bytes, two
// characters each.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		length := kit.IntRadix(buf.GetLatin1(8, 2), 16) * 2
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	typ := kit.IntRadix(kit.Substring(sentence, 6, 8), 16)
	if typ != 0x01 && typ != 0x02 && typ != 0x04 {
		return nil
	}
	buf := kit.NewBuf(kit.ParseHex(sentence))
	buf.Skip(5) // header, forced, device type, type, length

	imei := kit.SubstringFrom(hex.EncodeToString(buf.GetBytes(buf.WriterIndex()-9, 8)), 1)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(m.Now)

	pos.Set("height", buf.ReadUnsignedShort())
	pos.Valid = buf.ReadUnsignedByte() > 0
	pos.SetLongitude(float64(buf.ReadFloat()))
	pos.SetLatitude(float64(buf.ReadFloat()))
	pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedByte())
	pos.Set(model.KeyStatus, buf.ReadUnsignedInt())
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
	pos.Set(model.KeyRssi, buf.ReadFloat())
	pos.Set(model.KeyIndex, buf.ReadUnsignedShort())
	return pos
}
