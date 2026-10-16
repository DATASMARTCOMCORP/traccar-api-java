// Package intellitrac is the Intellitrac protocol (IntellitracProtocol): text lines,
// optionally after an 8-byte sync packet that is echoed back.
package intellitrac

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "intellitrac",
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

// Framer is IntellitracFrameDecoder: lines, after echoing a sync packet (0xFAF8...).
func Framer(maxLength int) ingest.Framer {
	lines := ingest.NewLineFramer(maxLength)
	return ingest.FramerFunc(func(m *ingest.Message, buf *kit.Buf) any {
		if buf.GetUnsignedShort(buf.ReaderIndex()) == 0xFAF8 {
			m.Send(buf.ReadSlice(8).Copy())
		}
		return lines.Frame(m, buf)
	})
}

var pattern = kit.NewPattern().
	Expression(".+,").Optional().
	Number("(d+),").          // identifier
	Number("(dddd)(dd)(dd)"). // date (yyyymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Number("(-?d+.d+),").     // longitude
	Number("(-?d+.d+),").     // latitude
	Number("(d+.?d*),").      // speed
	Number("(d+.?d*),").      // course
	Number("(-?d+.?d*),").    // altitude
	Number("(d+),").          // satellites
	Number("(d+),").          // event
	Number("(d+),").          // input
	Number("(d+),?").         // output
	Number("(d+.d+)?,?").     // adc1
	Number("(d+.d+)?,?").     // adc2
	GroupBegin().
	Number("d{14},d+,").
	Number("(d+),").   // vss
	Number("(d+),").   // rpm
	Number("(-?d+),"). // coolant
	Number("(d+),").   // fuel
	Number("(d+),").   // fuel consumption
	Number("(-?d+),"). // fuel temperature
	Number("(d+),").   // charger pressure
	Number("(d+),").   // tpl
	Number("(d+),").   // axle weight
	Number("(d+)").    // odometer
	GroupEndWith("?").
	Any().
	Compile()

func decodeAlarm(value int) string {
	switch value {
	case 164:
		return model.AlarmGeofenceEnter
	case 165:
		return model.AlarmGeofenceExit
	case 168, 169:
		return model.AlarmLowPower
	case 170:
		return model.AlarmPowerOff
	case 176:
		return model.AlarmPowerRestored
	case 180:
		return model.AlarmFallDown
	case 225:
		return model.AlarmJamming
	case 995:
		return model.AlarmSos
	}
	return ""
}

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
	pos.SetTime(parser.NextDateTime(kit.YMD_HMS, nil, m.Now))

	pos.Valid = true
	pos.SetLongitude(parser.MustDouble())
	pos.SetLatitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()
	pos.Altitude = parser.MustDouble()

	pos.Set(model.KeySatellites, parser.NextInt())
	event := parser.MustInt()
	pos.AddAlarm(decodeAlarm(event))
	pos.Set(model.KeyEvent, event)
	pos.Set(model.KeyInput, parser.NextInt())
	pos.Set(model.KeyOutput, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.NextDouble())
	pos.Set(model.PrefixAdc+"2", parser.NextDouble())

	// J1939 data
	if parser.HasNextN(10) {
		pos.Set(model.KeyObdSpeed, parser.NextInt())
		pos.Set(model.KeyRpm, parser.NextInt())
		pos.Set("coolant", parser.NextInt())
		pos.Set(model.KeyFuel, parser.NextInt())
		pos.Set(model.KeyFuelConsumption, parser.NextInt())
		pos.Set(model.PrefixTemp+"1", parser.NextInt())
		pos.Set("chargerPressure", parser.NextInt())
		pos.Set("tpl", parser.NextInt())
		pos.Set(model.KeyAxleWeight, parser.NextInt())
		pos.Set(model.KeyObdOdometer, parser.NextInt())
	}
	return pos
}
