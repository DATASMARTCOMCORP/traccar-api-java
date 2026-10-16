// Package autograde is the AutoGrade protocol (AutoGradeProtocol): "(...)" fixed-width
// text messages over TCP.
package autograde

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "autograde",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ")"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("(").
	Number("d{12}").              // index
	Number("(d{15})").            // imei
	Number("(dd)(dd)(dd)").       // date (ddmmyy)
	Expression("([AV])").         // validity
	Number("(d+)(dd.d+)([NS])").  // latitude
	Number("(d+)(dd.d+)([EW])").  // longitude
	Number("([d.]{5})").          // speed
	Number("(dd)(dd)(dd)").       // time (hhmmss)
	Number("([d.]{6})").          // course
	Expression("(.)").            // status
	Number("A(xxxx)").
	Number("B(xxxx)").
	Number("C(xxxx)").
	Number("D(xxxx)").
	Number("E(xxxx)").
	Number("K(xxxx)").
	Number("L(xxxx)").
	Number("M(xxxx)").
	Number("N(xxxx)").
	Number("O(xxxx)").
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

	day, month, year := parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0)
	date := kit.NewDateBuilder(nil).SetDateReverse(day, month, year)

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)

	hour, minute, second := parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0)
	date.SetTime(hour, minute, second)
	pos.SetTime(date.Date())

	pos.Course = parser.NextDoubleDefault(0)

	status := int(kit.CharAt(parser.Next(), 0))
	pos.Set(model.KeyStatus, status)
	pos.Set(model.KeyIgnition, kit.Check(int64(status), 0))

	for i := 1; i <= 5; i++ {
		pos.Set(model.PrefixAdc+strconv.Itoa(i), parser.Next())
	}
	for i := 1; i <= 5; i++ {
		pos.Set("can"+strconv.Itoa(i), parser.Next())
	}
	return pos
}
