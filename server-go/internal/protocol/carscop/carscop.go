// Package carscop is the Carscop protocol (CarscopProtocol): "*...^" text reports over TCP.
package carscop

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "carscop",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "^"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("*").
	Any().
	Number("(dd)(dd)(dd)").  // time (hhmmss)
	Expression("([AV])").    // validity
	Number("(dd)(dd.dddd)"). // latitude
	Expression("([NS])").
	Number("(ddd)(dd.dddd)"). // longitude
	Expression("([EW])").
	Number("(ddd.d)").      // speed
	Number("(dd)(dd)(dd)"). // date (yymmdd)
	Number("(ddd.dd)").     // course
	GroupBegin().
	Number("(d{8})").  // state
	Number("L(d{6})"). // odometer
	GroupEndWith("?").
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	var ds *ingest.DeviceSession
	if index := kit.IndexOf(sentence, "UB05"); index != -1 {
		ds = m.Session(kit.Substring(sentence, index+4, index+4+15))
	} else {
		ds = m.Session()
	}
	if ds == nil {
		return nil
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).
		SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)

	date.SetDate(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())

	pos.Course = parser.NextDoubleDefault(0)

	if parser.HasNextN(2) {
		pos.Set(model.KeyStatus, parser.Next())
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
	}
	return pos
}
