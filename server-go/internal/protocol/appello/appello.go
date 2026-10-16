// Package appello is the Appello protocol (AppelloProtocol): "FOLLOWIT," text lines over
// TCP.
package appello

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "appello",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("FOLLOWIT,").            // brand
	Number("(d+),").              // imei
	GroupBegin().
	Number("(dd)(dd)(dd)").       // date (yymmdd)
	Number("(dd)(dd)(dd).?d*,").  // time (hhmmss)
	Or().
	Text("UTCTIME,").
	GroupEnd().
	Number("(-?d+.d+),").         // latitude
	Number("(-?d+.d+),").         // longitude
	Number("(d+),").              // speed
	Number("(d+),").              // course
	Number("(d+),").              // satellites
	Number("(-?d+),").            // altitude
	Expression("([FL]),").        // gps state
	Any().
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

	if parser.HasNextN(6) {
		pos.SetTime(parser.NextDateTimeDefault())
	} else {
		m.LastLocation(pos, nil)
	}

	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))

	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Valid = parser.Next() == "F"
	return pos
}
