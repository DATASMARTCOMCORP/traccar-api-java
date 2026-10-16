// Package esky is the Esky protocol (EskyProtocol): ';' and '+' separated text messages,
// over TCP and UDP.
package esky

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP (framed) and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "esky",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Framer: Framer(), Text: true, TextOut: true, Decoder: &Decoder{}}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Text: true, TextOut: true, Decoder: &Decoder{Datagram: true}}
			}},
		},
	}
}

// Framer is EskyFrameDecoder: a frame starts at an 'E' and ends before the next one (or
// takes everything there is).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		startIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 'E')
		if startIndex >= 0 {
			buf.SetReaderIndex(startIndex)
			endIndex := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), 'E')
			if endIndex > 0 {
				return buf.ReadSlice(endIndex - buf.ReaderIndex())
			}
			return buf.ReadSlice(buf.ReadableBytes()) // assume full frame
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Expression("..;").          // header
	Number("d+;").              //
	Number("(d+);").            // imei
	Text("R;").                 // data type
	Number("(d+)[+;]").         // satellites
	Number("(dd)(dd)(dd)").     // date
	Number("(dd)(dd)(dd)[+;]"). // time
	Number("(-?d+.d+)[+;]").    // latitude
	Number("(-?d+.d+)[+;]").    // longitude
	Number("(d+.d+)[+;]").      // speed
	Number("(d+)[+;]").         // course
	GroupBegin().
	Text("0x").Number("(x+)[+;]"). // input
	Number("(d+)[+;]").            // message type
	Number("(d+)[+;]").            // odometer
	GroupEndWith("?").
	Number("(d+)[+;]"). // adc 1
	Number("(d+)").     // voltage
	Any().
	Compile()

// Decoder is EskyProtocolDecoder; Datagram is set on the UDP server, where messages are
// acknowledged.
type Decoder struct {
	Datagram bool
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeySatellites, parser.NextInt())

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromMps(parser.MustDouble())
	pos.Course = parser.MustDouble()

	if parser.HasNextN(3) {
		input := int64(parser.MustHexInt())
		pos.Set(model.KeyIgnition, !kit.Check(input, 0))
		pos.Set(model.PrefixIn+"1", !kit.Check(input, 1))
		pos.Set(model.PrefixIn+"2", !kit.Check(input, 2))
		pos.Set(model.KeyEvent, parser.NextInt())
		pos.Set(model.KeyOdometer, parser.NextInt())
	}

	pos.Set(model.PrefixAdc+"1", parser.NextInt())
	pos.Set(model.KeyBattery, float64(parser.MustInt())/100.0)

	if index := strings.LastIndexByte(sentence, '+'); index > 0 && d.Datagram {
		m.Send("ACK," + sentence[index+1:] + "#")
	}
	return pos
}
