// Package cartrack is the CarTrack protocol (CarTrackProtocol): "$$...##" text reports
// over TCP.
package cartrack

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "cartrack",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "##"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("$$").     // header
	Number("(d+)"). // device id
	Text("?").Expression("*").
	Text("&A").
	Number("(dddd)"). // command
	Text("&B").
	Number("(dd)(dd)(dd).(ddd),"). // time (hhmmss.sss)
	Expression("([AV]),").         // validity
	Number("(dd)(dd.dddd),").      // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.dddd),"). // longitude
	Expression("([EW]),").
	Number("(d+.d*)?,").    // speed
	Number("(d+.d*)?,").    // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	Any().
	Expression("&C([^&]*)").            // io
	Expression("&D([^&]*)").            // odometer
	Expression("&E([^&]*)").            // alarm
	Expression("&Y([^&]*)").Optional(). // adc
	Compile()

var odometerDigits = strings.NewReplacer(":", "A", ";", "B", "<", "C", "=", "D", ">", "E", "?", "F")

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

	pos.Set(model.KeyCommand, parser.NextNullable())

	date := kit.NewDateBuilder(nil).SetTimeMillis(
		parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())

	pos.Set(model.PrefixIo+"1", parser.NextNullable())

	odometer := odometerDigits.Replace(parser.Next())
	pos.Set(model.KeyOdometer, kit.IntRadix(odometer, 16))

	parser.Next() // there is no meaningful alarms
	pos.Set(model.PrefixAdc+"1", parser.NextNullable())

	return pos
}
