// Package raveon is the Raveon protocol (RaveonProtocol): $PRAVE text lines.
package raveon

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "raveon",
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
	Text("$PRAVE,").
	Number("(d+),"). // id
	Number("d+,").
	Number("(-?)(d+)(dd.d+),").   // latitude
	Number("(-?)(d+)(dd.d+),").   // longitude
	Number("(dd)(dd)(dd),").      // time (hhmmss)
	Number("(d),").               // validity
	Number("(d+),").              // satellites
	Number("(-?d+),").            // altitude
	Number("(-?d+),").            // temperature
	Number("(d+.d+),").           // power
	Number("(d+),").              // inputs
	Number("(-?d+),").            // gsm
	Number("(d+),").              // speed
	Number("(d+),").              // course
	Expression("([PMACIVSX])?,"). // status
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

	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetTime(parser.NextDateTime(kit.HMS, nil, m.Now))
	pos.Valid = parser.NextIntDefault(0) != 0
	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Altitude = float64(parser.NextIntDefault(0))
	pos.Set(model.PrefixTemp+"1", parser.NextIntDefault(0))
	pos.Set(model.KeyPower, parser.NextDoubleDefault(0))
	pos.Set(model.KeyInput, parser.NextIntDefault(0))
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))
	pos.Speed = units.KnotsFromKph(float64(parser.NextIntDefault(0)))
	pos.Course = float64(parser.NextIntDefault(0))
	if alarm := parser.NextNullable(); alarm != nil {
		pos.AddAlarm(*alarm)
	}
	return pos
}
