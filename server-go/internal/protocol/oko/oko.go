// Package oko is the OKO protocol (OkoProtocol): "{...}" text reports over TCP.
package oko

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "oko",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "}"),
				Text:    true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("{").
	Number("(d{15}),").Optional().   // imei
	Number("(dd)(dd)(dd)(?:.d+)?,"). // time
	Expression("([AV]),").           // validity
	Number("(dd)(dd.d+),").          // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").       // speed
	Number("(d+.?d*)?,").       // course
	Number("(dd)(dd)(dd),").    // date (ddmmyy)
	Number("(d+),").            // satellites
	Number("(d+.d+|xx),").      // adc
	Number("(xx),").            // event
	Number("(d+.d+|xx),").      // power
	Number("d").                // memory status
	Number(",(xx)").Optional(). // io
	Any().
	Compile()

func decodeVoltage(parser *kit.Parser) float64 {
	value := parser.Next()
	if strings.Contains(value, ".") {
		return kit.Double(value)
	}
	return float64(kit.IntRadix(value, 16)) / 10.0
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	var ds *ingest.DeviceSession
	if parser.HasNext() {
		ds = m.Session(parser.Next())
	} else {
		ds = m.Session()
	}
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	b.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(b.Date())

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", decodeVoltage(parser))
	pos.Set(model.KeyEvent, parser.Next())
	pos.Set(model.KeyPower, decodeVoltage(parser))
	pos.Set(model.KeyInput, parser.NextHexInt())
	return pos
}
