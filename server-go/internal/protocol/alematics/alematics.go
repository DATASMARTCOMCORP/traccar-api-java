// Package alematics is the Alematics protocol (AlematicsProtocol): "$T," text lines over
// TCP, with heartbeats that are echoed back.
package alematics

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "alematics",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is AlematicsFrameDecoder: lines, after echoing a 12-byte heartbeat (0xFAF8...).
func Framer(maxLength int) ingest.Framer {
	lines := ingest.NewLineFramer(maxLength)
	return ingest.FramerFunc(func(m *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}
		if buf.GetUnsignedShort(buf.ReaderIndex()) == 0xFAF8 {
			m.Send(buf.ReadSlice(12).Copy())
		}
		return lines.Frame(m, buf)
	})
}

var pattern = kit.NewPattern().
	Text("$T,").
	Number("(d+),").          // type
	Number("(d+),").          // index
	Number("(d+),").          // id
	Number("(dddd)(dd)(dd)"). // gps date
	Number("(dd)(dd)(dd),").  // gps time
	Number("(dddd)(dd)(dd)"). // device date
	Number("(dd)(dd)(dd),").  // device time
	Number("(-?d+.d+),").     // latitude
	Number("(-?d+.d+),").     // longitude
	Number("(d+),").          // speed
	Number("(d+),").          // course
	Number("(-?d+),").        // altitude
	Number("(d+.d),").        // hdop
	Number("(d+),").          // satellites
	Number("(d+),").          // input
	Number("(d+),").          // output
	Number("(d+.d+),").       // adc
	Number("(d+.d+),").       // power
	Number("(d+),").          // odometer
	GroupBegin().
	Text("0,$S,").
	Expression("(.*)"). // text message
	Or().
	Number("(d+),").    // extra mask
	Expression("(.*)"). // extra data
	Or().
	Any().
	GroupEnd().
	Compile()

func decodeExtras(pos *model.Position, parser *kit.Parser) {
	mask := int64(parser.MustInt())
	data := kit.Split(parser.Next(), ",")
	index := 0
	next := func() int {
		v := kit.Int(data[index])
		index++
		return v
	}
	if kit.Check(mask, 0) {
		index++ // pulse counter 3
	}
	if kit.Check(mask, 1) {
		pos.Set(model.KeyPower, next())
	}
	if kit.Check(mask, 2) {
		pos.Set(model.KeyBattery, next())
	}
	if kit.Check(mask, 3) {
		pos.Set(model.KeyObdSpeed, next())
	}
	if kit.Check(mask, 4) {
		pos.Set(model.KeyRpm, next())
	}
	if kit.Check(mask, 5) {
		pos.Set(model.KeyRssi, next())
	}
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.Set(model.KeyType, parser.NextInt())
	pos.Set(model.KeyIndex, parser.NextInt())

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.FixTime = model.NewTime(parser.NextDateTimeDefault())
	pos.DeviceTime = model.NewTime(parser.NextDateTimeDefault())

	pos.Valid = true
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Speed = units.KnotsFromKph(float64(parser.NextIntDefault(0)))
	pos.Course = float64(parser.NextIntDefault(0))
	pos.Altitude = float64(parser.NextIntDefault(0))

	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyInput, parser.NextInt())
	pos.Set(model.KeyOutput, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.NextDouble())
	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyOdometer, parser.NextInt())

	if parser.HasNext() {
		pos.Set("text", parser.Next())
	} else if parser.HasNext() {
		decodeExtras(pos, parser)
	}
	return pos
}
