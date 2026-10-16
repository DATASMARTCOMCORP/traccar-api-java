// Package smartsole is the SmartSole protocol (SmartSoleProtocol): "#GTXRP=" text reports
// delimited by '$'.
package smartsole

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "smartsole",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "$"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("#GTXRP=").
	Number("(d+),").         // imei
	Number("d+,").           // report type
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+),").       // altitude
	Number("(d+),").         // speed
	Number("([01]),").       // valid
	Number("(d+),").         // satellites
	Number("(d+.d+),").      // hdop
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(d+.d+),").      // battery
	Number("(d+)").          // status
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

	pos.FixTime = model.NewTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Altitude = float64(parser.MustInt())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Valid = parser.MustInt() == 1

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextDouble())

	pos.DeviceTime = model.NewTime(parser.NextDateTimeDefault())

	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyStatus, parser.NextInt())

	return pos
}
