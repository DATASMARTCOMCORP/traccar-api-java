// Package dway is the Dway protocol (DwayProtocol): comma-separated text lines over TCP.
package dway

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "dway",
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
	Text("AA55,").
	Number("d+,").           // index
	Number("(d+),").         // imei
	Number("d+,").           // type
	Number("(dd)(dd)(dd),"). // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+),").       // altitude
	Number(" ?(d+.d+),").    // speed
	Number("(d+),").         // course
	Number("([01]{4}),").    // input
	Number("([01]{4}),").    // output
	Number("([01]+),").      // flags
	Number("(d+),").         // battery
	Number("(d+),").         // adc1
	Number("(d+),").         // adc2
	Number("(d+)").          // driver
	Any().
	Compile()

// decode follows the Java decoder, which never reads the flags group: the battery is
// read from the flags, the ADCs from the battery and adc1, the driver from adc2.
func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	if sentence == "AA55,HB" {
		m.Send("55AA,HB,OK\r\n")
		return nil
	}

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

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyInput, parser.NextBinInt())
	pos.Set(model.KeyOutput, parser.NextBinInt())

	pos.Set(model.KeyBattery, float64(parser.MustInt())/1000.0)
	pos.Set(model.PrefixAdc+"1", float64(parser.MustInt())/1000.0)
	pos.Set(model.PrefixAdc+"2", float64(parser.MustInt())/1000.0)
	pos.Set(model.KeyDriverUniqueID, parser.Next())
	return pos
}
