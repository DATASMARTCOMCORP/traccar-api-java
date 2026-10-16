// Package portman is the Portman protocol (PortmanProtocol): text lines in a standard
// (%% or $PTMLA) and an extended ($EXT) format.
package portman

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "portman",
		DataCommands: protocol.DataCommands("portman"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var patternStandard = kit.NewPattern().
	GroupBegin().
	Text("$PTMLA,"). // header
	Or().
	Text("%%"). // header
	GroupEnd().
	Expression("([^,]+),").  // id
	Expression("([ABCL]),"). // validity
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([NS])").
	Number("(dd)(dd.d+)"). // latitude
	Expression("([EW])").
	Number("(d{2,3})(dd.d+),"). // longitude
	Number("(d+),").            // speed
	Number("(d+),").            // course
	Number("(?:NA|C(-?d+)),").  // temperature
	Number("(x{8}),").          // status
	GroupBegin().
	Text("NA").
	Or().
	Number("F(d+)"). // fuel
	Or().
	Number("(d+)"). // card id
	GroupEndWith(",").
	Number("(d+),").             // event
	Number("(d+),").             // satellites
	Number("(d+.d+),").          // odometer
	Number("(d+)").              // rssi
	Number(",G(d+)").Optional(). // fuel
	Any().
	Compile()

var patternExtended = kit.NewPattern().
	Text("$EXT,").           // header
	Expression("([^,]+),").  // id
	Expression("([ABCL]),"). // validity
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([NS])").
	Number("(dd)(dd.d+)"). // latitude
	Expression("([EW])").
	Number("(d{2,3})(dd.d+),"). // longitude
	Number("(d+),").            // speed
	Number("(d+),").            // course
	Number("(?:NA|C(-?d+)),").  // temperature
	Number("(?:NA|F(d+)),").    // fuel
	Number("(d+),").            // satellites
	Number("(d+),").            // rssi
	Number("(d+.d+),").         // odometer
	Number("(?:NA|(d+)),").     // card id
	Number("(x{8}),").          // status
	Number("(d+)").             // event
	Any().
	Compile()

// start parses the fields common to both formats: id through course.
func start(m *ingest.Message, parser *kit.Parser) *model.Position {
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Valid = parser.Next() != "L"
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	return pos
}

func decodeStandard(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternStandard, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := start(m, parser)
	if pos == nil {
		return nil
	}

	pos.Set(model.PrefixTemp+"1", parser.NextNullable())
	pos.Set(model.KeyStatus, parser.NextHexLong())
	if parser.HasNext() {
		pos.Set(model.KeyFuel, float64(parser.MustInt())/10.0)
	}
	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())

	event := parser.MustInt()
	pos.Set(model.KeyEvent, event)
	switch event {
	case 253:
		pos.Set(model.KeyIgnition, true)
	case 254:
		pos.Set(model.KeyIgnition, false)
	}

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeyFuel, parser.NextInt())

	return pos
}

func decodeExtended(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternExtended, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := start(m, parser)
	if pos == nil {
		return nil
	}

	pos.Set(model.PrefixTemp+"1", parser.NextNullable())
	pos.Set(model.KeyFuel, parser.NextInt())
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())
	pos.Set(model.KeyStatus, parser.NextHexLong())
	pos.Set(model.KeyEvent, parser.NextInt())

	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	switch {
	case strings.HasPrefix(sentence, "%%"), strings.HasPrefix(sentence, "$PTMLA"):
		return decodeStandard(m, sentence)
	case strings.HasPrefix(sentence, "$EXT"):
		return decodeExtended(m, sentence)
	}
	return nil
}

// encode is PortmanProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "&&%s,XA5\r\n", model.CommandKeyUniqueID)
	case model.CommandEngineResume:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "&&%s,XA6\r\n", model.CommandKeyUniqueID)
	}
	return nil
}
