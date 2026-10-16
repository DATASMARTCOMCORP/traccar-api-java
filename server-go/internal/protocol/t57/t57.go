// Package t57 is the T57 protocol (T57Protocol): '#'-separated "*T57#" text records over
// TCP, framed by counting separators.
package t57

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "t57",
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

// Framer is T57FrameDecoder: a frame ends at the 12th (type F3) or 14th '#'.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: frame}
}

func frame(_ *ingest.Message, buf *kit.Buf) any {
	if buf.ReadableBytes() < 10 {
		return nil
	}
	typ := buf.GetString(buf.ReaderIndex()+5, 2)
	count := 14
	if typ == "F3" {
		count = 12
	}
	index := 0
	// The search starts at index 1 of the buffer, not at its reader index, as in Java.
	for index >= 0 && count > 0 {
		index = buf.IndexOf(index+1, buf.WriterIndex(), '#')
		if index > 0 {
			count--
		}
	}
	if index > 0 {
		return buf.ReadSlice(index + 1 - buf.ReaderIndex())
	}
	return nil
}

var pattern = kit.NewPattern().
	Text("*T57#").
	Number("Fd#").           // type
	Number("([^#]+)#").      // device id
	Number("(dd)(dd)(dd)#"). // date (ddmmyy)
	Number("(dd)(dd)(dd)#"). // time (hhmmss)
	Number("(dd)(dd.d+)#").  // latitude
	Expression("([NS])#").
	Number("(ddd)(dd.d+)#"). // longitude
	Expression("([EW])#").
	Expression("[^#]+#").
	Number("(d+.d+)#").   // speed
	Number("(d+.d+)#").   // altitude
	Expression("([AV])"). // valid
	Number("d#").         // fix type
	Number("(d+.d+)#").   // battery
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.MustDouble()
	pos.Altitude = parser.MustDouble()
	pos.Valid = parser.Next() == "A"
	pos.Set(model.KeyBattery, parser.NextDouble())

	return pos
}
