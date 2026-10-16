// Package m2c is the M2C protocol (M2cProtocol): '['-started batches of "#M2C," text
// lines, ended by ']'.
package m2c

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "m2c",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLengthLarge, "]"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("#M2C,").
	Expression("[^,]+,").    // model
	Expression("[^,]+,").    // firmware
	Number("d+,").           // protocol
	Number("(d+),").         // imei
	Number("(d+),").         // index
	Expression("([LH]),").   // archive
	Number("d+,").           // priority
	Number("(d+),").         // event
	Number("(dd)(dd)(dd),"). // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+),").       // altitude
	Number("(d+),").         // course
	Number("(d+.d+),").      // speed
	Number("(d+),").         // satellites
	Number("(d+),").         // odometer
	Number("(d+),").         // input
	Number("(d+),").         // output
	Number("(d+),").         // power
	Number("(d+),").         // battery
	Number("(d+),").         // adc 1
	Number("(d+),").         // adc 2
	Number("(d+.?d*),").     // temperature
	Any().
	Compile()

func decodePosition(m *ingest.Message, line string) *model.Position {
	parser := kit.NewParser(pattern, line)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyIndex, parser.NextInt())
	if parser.Next() == "H" {
		pos.Set(model.KeyArchive, true)
	}
	pos.Set(model.KeyEvent, parser.NextInt())

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Altitude = float64(parser.MustInt())
	pos.Course = float64(parser.MustInt())
	pos.Speed = units.KnotsFromKph(parser.MustDouble())

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyOdometer, parser.NextLong())
	pos.Set(model.KeyInput, parser.NextInt())
	pos.Set(model.KeyOutput, parser.NextInt())
	pos.Set(model.KeyPower, float64(parser.MustInt())/1000.0)
	pos.Set(model.KeyBattery, float64(parser.MustInt())/1000.0)
	pos.Set(model.PrefixAdc+"1", parser.NextInt())
	pos.Set(model.PrefixAdc+"2", parser.NextInt())
	pos.Set(model.PrefixTemp+"1", parser.NextDouble())
	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := kit.SubstringFrom(msg.(string), 1) // remove start symbol
	positions := []*model.Position{}
	for _, line := range kit.Split(sentence, "\r\n") {
		if line != "" {
			if pos := decodePosition(m, line); pos != nil {
				positions = append(positions, pos)
			}
		}
	}
	return positions
}
