// Package ais is the AIS protocol (AisProtocol): NMEA !AIVDM sentences over TCP, whose
// 6-bit payloads carry vessel position reports.
package ais

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "ais",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Text: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("!AIVDM,").
	Number("(d+),").          // count
	Number("(d+),").          // index
	Number("(d+)?,").         // id
	Expression(".,").         // radio channel
	Expression("([^,]+),").   // payload
	Any().
	Compile()

func decodePayload(m *ingest.Message, buf *kit.BitBuffer) *model.Position {
	typ := buf.ReadUnsigned(6)
	if typ != 1 && typ != 2 && typ != 3 && typ != 18 {
		return nil
	}
	buf.ReadUnsigned(2)
	mmsi := buf.ReadUnsigned(30)
	ds := m.Session(strconv.Itoa(mmsi))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(m.Now)

	if typ == 18 {
		buf.ReadUnsigned(8) // reserved
	} else {
		pos.Set(model.KeyStatus, buf.ReadUnsigned(4))
		pos.Set("turn", buf.ReadSigned(8))
	}

	pos.Speed = float64(buf.ReadUnsigned(10)) / 10.0
	pos.Valid = buf.ReadUnsigned(1) != 0
	pos.SetLongitude(float64(buf.ReadSigned(28)) / 10000.0 / 60.0)
	pos.SetLatitude(float64(buf.ReadSigned(27)) / 10000.0 / 60.0)
	pos.Course = float64(buf.ReadUnsigned(12)) / 10.0
	pos.Set("heading", buf.ReadUnsigned(9))
	return pos
}

func decode(m *ingest.Message, msg any) any {
	positions := []*model.Position{}
	buffers := map[int]*kit.BitBuffer{}
	for _, sentence := range kit.Split(msg.(string), "\r\n") {
		if sentence == "" {
			continue
		}
		parser := kit.NewParser(pattern, sentence)
		if !parser.Matches() {
			continue
		}
		count := parser.NextIntDefault(0)
		index := parser.NextIntDefault(0)
		id := parser.NextIntDefault(0)

		var pos *model.Position
		if count == 1 {
			bits := kit.NewBitBuffer(nil)
			bits.WriteEncoded(kit.ASCIIBytes(parser.Next()))
			pos = decodePayload(m, bits)
		} else {
			bits := buffers[id]
			if bits == nil {
				bits = kit.NewBitBuffer(nil)
				buffers[id] = bits
			}
			bits.WriteEncoded(kit.ASCIIBytes(parser.Next()))
			if count == index {
				pos = decodePayload(m, bits)
				delete(buffers, id)
			}
		}
		if pos != nil {
			positions = append(positions, pos)
		}
	}
	return positions
}
