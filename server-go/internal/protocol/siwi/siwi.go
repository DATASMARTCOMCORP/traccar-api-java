// Package siwi is the Siwi protocol (SiwiProtocol): "$..." comma-separated text lines.
package siwi

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "siwi",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("$").Expression("[A-Z]+,"). // header
	Number("(d+),").                 // device id
	Number("d+,").                   // unit no
	Expression("([A-Z]),").          // reason
	Number("d*,").                   // command code
	Number("[^,]*,").                // command value
	Expression("([01]),").           // ignition
	Expression("[01],").             // power cut
	Number("d+,").                   // flags
	Number("[^,]+,").
	Number("(d+),").         // odometer
	Number("(d+),").         // speed
	Number("(d+),").         // satellites
	Expression("([AV]),").   // valid
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+),").       // altitude
	Number("(d+),").         // course
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("d+,").           // signal strength
	Number("d+,").           // gsm status
	Number("d+,").           // error code
	Number("d+,").           // internal status
	Number("(d+),").         // battery
	Number("(d+),").         // adc
	Number("(d+),").         // digital inputs
	Number("(d+),").         // sensor 1
	Number("(d+),").         // sensor 2
	Number("(d+),").         // sensor 3
	Number("(d+),").         // sensor 4
	Expression("([^,]+),").  // hw version
	Expression("([^,]+),").  // sw version
	Any().
	Compile()

var zone = javatime.TimeZone("Asia/Kolkata")

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

	pos.Set(model.KeyEvent, parser.Next())
	pos.Set(model.KeyIgnition, parser.Next() == "1")
	pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
	pos.Speed = units.KnotsFromKph(float64(parser.NextIntDefault(0)))
	pos.Set(model.KeySatellites, parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Course = float64(parser.NextIntDefault(0))

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, zone, m.Now))

	pos.Set(model.KeyBattery, float64(parser.MustInt())/1000.0)
	pos.Set(model.PrefixAdc+"1", float64(parser.MustInt())/100.0)
	pos.Set(model.KeyInput, parser.NextInt())

	for i := 1; i <= 4; i++ {
		if value := parser.MustInt(); value != 0 {
			pos.Set(model.PrefixIo+strconv.Itoa(i), value)
		}
	}

	pos.Set(model.KeyVersionHw, parser.Next())
	pos.Set(model.KeyVersionFw, parser.Next())

	return pos
}
