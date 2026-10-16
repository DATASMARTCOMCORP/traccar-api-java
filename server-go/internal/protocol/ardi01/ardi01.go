// Package ardi01 is the ARDI-01 protocol (Ardi01Protocol): comma-separated text lines over
// TCP.
package ardi01

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "ardi01",
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
	Number("(d+),").          // imei
	Number("(dddd)(dd)(dd)"). // date (yyyymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Number("(-?d+.d+),").     // longitude
	Number("(-?d+.d+),").     // latitude
	Number("(d+.?d*),").      // speed
	Number("(d+.?d*),").      // course
	Number("(-?d+.?d*),").    // altitude
	Number("(d+),").          // satellites
	Number("(d+),").          // event
	Number("(d+),").          // battery
	Number("(-?d+)").         // temperature
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

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)

	satellites := parser.NextIntDefault(0)
	pos.Valid = satellites >= 3
	pos.Set(model.KeySatellites, satellites)

	pos.Set(model.KeyEvent, parser.Next())
	pos.Set(model.KeyBatteryLevel, parser.NextIntDefault(0))
	pos.Set(model.PrefixTemp+"1", parser.Next())
	return pos
}
