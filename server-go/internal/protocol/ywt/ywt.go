// Package ywt is the Ywt protocol (YwtProtocol): "%XX,..." text lines over TCP.
package ywt

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "ywt",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(Decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Expression("%(..),").    // type
	Number("(d+):").         // unit identifier
	Number("d+,").           // subtype
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([EW])").
	Number("(ddd.d{6}),"). // longitude
	Expression("([NS])").
	Number("(dd.d{6}),").          // latitude
	Number("(d+)?,").              // altitude
	Number("(d+),").               // speed
	Number("(d+),").               // course
	Number("(d+),").               // satellite
	Expression("([^,]+),").        // report identifier
	Expression("([-0-9a-fA-F]+)"). // status
	Any().
	Compile()

// Decode is YwtProtocolDecoder.
func Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	// Synchronization
	if strings.HasPrefix(sentence, "%SN") {
		start := kit.IndexOf(sentence, ":")
		end := start
		for i := 0; i < 4; i++ {
			end = kit.IndexOfFrom(sentence, ",", end+1)
		}
		if end == -1 {
			end = kit.Len(sentence)
		}
		m.Send("%AT+SN=" + kit.Substring(sentence, start, end))
		return nil
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	typ := parser.Next()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()

	satellites := parser.MustInt()
	pos.Valid = satellites != 0
	pos.Set(model.KeySatellites, satellites)

	reportID := parser.Next()

	pos.Set(model.KeyStatus, parser.NextNullable())

	// Send response
	if typ == "KP" || typ == "EP" {
		m.Send("%AT+" + typ + "=" + reportID + "\r\n")
	}

	return pos
}
