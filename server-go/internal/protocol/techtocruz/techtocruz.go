// Package techtocruz is the TechtoCruz protocol (TechtoCruzProtocol): "$$A<length>,..."
// text messages over TCP.
package techtocruz

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "techtocruz",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is TechtoCruzFrameDecoder: the decimal length after "$$A" counts from the
// length field's start.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: frame}
}

func frame(_ *ingest.Message, buf *kit.Buf) any {
	lengthStart := buf.ReaderIndex() + 3
	lengthEnd := buf.IndexOf(lengthStart, buf.WriterIndex(), ',')
	if lengthEnd > 0 {
		// As in Java, the length is compared with the readable bytes as an index.
		length := lengthStart + kit.Int(buf.GetString(lengthStart, lengthEnd-lengthStart))
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
	}
	return nil
}

var pattern = kit.NewPattern().
	Text("$$A").
	Number("d+,").             // length
	Number("(d+),").           // imei
	Number("(dd)(dd)(dd)").    // date (yymmdd)
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Expression("([AV]),").     // valid
	Expression("[^,]+,").      // manufacturer
	Expression("([^,]+),").    // license plate
	Number("(d+.d+),").        // speed
	Number("(d+),").           // odometer
	Number("(-?d+.d+),[NS],"). // latitude
	Number("(-?d+.d+),[WE],"). // longitude
	Number("(-?d+.d+),").      // altitude
	Number("(d+.d+),").        // course
	Number("(d+),").           // satellites
	Number("(d+),").           // rssi
	Number("(d+.d+),").        // power
	Number("(d+.d+),").        // battery
	Number("([01]),").         // charge
	Number("[01],").           // speed sensor
	Number("[01],").           // gps status
	Number("([01]),").         // ignition
	Number("([01]),").         // overspeed
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

	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"
	pos.Set("registration", parser.Next())
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Set(model.KeyOdometer, parser.NextInt())
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Altitude = parser.MustDouble()
	pos.Course = parser.MustDouble()
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyCharge, parser.MustInt() > 0)
	pos.Set(model.KeyIgnition, parser.MustInt() > 0)
	if parser.MustInt() > 0 {
		pos.AddAlarm(model.AlarmOverspeed)
	}

	return pos
}
