// Package jpkorjar is the JP-Korjar protocol (JpKorjarProtocol): "KORJAR.PL," text
// messages over TCP.
package jpkorjar

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "jpkorjar",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is JpKorjarFrameDecoder: at least 80 bytes, up to the first ',' after a space.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 80 {
			return nil
		}
		spaceIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ' ')
		if spaceIndex == -1 {
			return nil
		}
		endIndex := buf.IndexOf(spaceIndex, buf.WriterIndex(), ',')
		if endIndex == -1 {
			return nil
		}
		// readRetainedSlice(endIndex + 1): Java's length is the absolute index plus one
		return buf.ReadSlice(endIndex + 1)
	}}
}

var pattern = kit.NewPattern().
	Text("KORJAR.PL,").
	Number("(d+),").          // imei
	Number("(dd)(dd)(dd)").   // date (yymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Number("(d+.d+)([NS]),"). // latitude
	Number("(d+.d+)([EW]),"). // longitude
	Number("(d+.d+),").       // speed
	Number("(d+),").          // course
	Number("[FL]:(d+.d+)V,"). // battery
	Number("([01]) ").        // valid
	Number("(d+) ").          // mcc
	Number("(d+) ").          // mnc
	Number("(x+) ").          // lac
	Number("(x+),").          // cid
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.YMD_HMS, nil, m.Now))

	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0))

	pos.Valid = parser.NextIntDefault(0) == 1

	mcc := parser.NextIntDefault(0)
	mnc := parser.NextIntDefault(0)
	lac := parser.NextHexIntDefault(0)
	cid := parser.NextHexIntDefault(0)
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	return pos
}
