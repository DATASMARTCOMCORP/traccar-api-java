// Package box is the Box protocol (BoxProtocol): comma-separated text lines ending with
// '\r' over TCP.
package box

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "box",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\r"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("L,").
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Text("G,").
	Number("(-?d+.d+),"). // latitude
	Number("(-?d+.d+),"). // longitude
	Number("(d+.?d*),").  // speed
	Number("(d+.?d*),").  // course
	Number("(d+.?d*),").  // distance
	Number("(d+),").      // event
	Number("(d+)").       // status
	GroupBegin().
	Text(";").
	Expression("(.+)").
	GroupEndWith("?").
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	switch {
	case strings.HasPrefix(sentence, "H,"):
		index := kit.IndexOfFrom(sentence, ",", 2) + 1
		m.Session(kit.Substring(sentence, index, kit.IndexOfFrom(sentence, ",", index)))

	case strings.HasPrefix(sentence, "E,"):
		m.Send("A," + kit.SubstringFrom(sentence, 2) + "\r")

	case strings.HasPrefix(sentence, "L,"):
		ds := m.Session()
		if ds == nil {
			return nil
		}
		parser := kit.NewParser(pattern, sentence)
		if !parser.Matches() {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(parser.NextDateTimeDefault())

		pos.SetLatitude(parser.MustDouble())
		pos.SetLongitude(parser.MustDouble())
		pos.Speed = units.KnotsFromKph(parser.MustDouble())
		pos.Course = parser.MustDouble()

		pos.Set(model.KeyOdometerTrip, parser.MustDouble()*1000)
		pos.Set(model.KeyEvent, parser.Next())

		status := parser.MustInt()
		pos.Set(model.KeyIgnition, kit.Check(int64(status), 0))
		pos.Set(model.KeyMotion, kit.Check(int64(status), 1))
		pos.Valid = !kit.Check(int64(status), 2)
		pos.Set(model.KeyStatus, status)

		if parser.HasNext() {
			for _, item := range kit.Split(parser.Next(), ";") {
				valueIndex := kit.IndexOf(item, ",")
				pos.Set(strings.ToLower(kit.Substring(item, 0, valueIndex)), kit.SubstringFrom(item, valueIndex+1))
			}
		}
		return pos
	}
	return nil
}
