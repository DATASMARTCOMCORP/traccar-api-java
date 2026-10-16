// Package aquila is the Aquila protocol (AquilaProtocol): "$$" text lines over TCP, in
// several field layouts.
package aquila

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
		Name: "aquila",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("$$").
	Expression("[^,]*,").     // client
	Number("(d+),").          // device serial number
	Number("(d+),").          // event
	Number("(-?d+.d+),").     // latitude
	Number("(-?d+.d+),").     // longitude
	Number("(dd)(dd)(dd)").   // date (yymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Expression("([AV]),").    // validity
	GroupBegin().
	Number("(d+),").          // gsm
	Number("(d+),").          // speed
	Number("(d+),").          // distance
	GroupBegin().
	Number("d+,").            // driver code
	Number("(d+),").          // fuel
	Number("([01]),").        // io 1
	Number("[01],").          // case open switch
	Number("[01],").          // over speed start
	Number("[01],").          // over speed end
	Number("(?:d+,){3}").     // reserved
	Number("([01]),").        // power status
	Number("([01]),").        // io 2
	Number("d+,").            // reserved
	Number("([01]),").        // ignition
	Number("[01],").          // ignition off event
	Number("(?:d+,){7}").     // reserved
	Number("[01],").          // corner packet
	Number("(?:d+,){8}").     // reserved
	Number("([01]),").        // course bit 0
	Number("([01]),").        // course bit 1
	Number("([01]),").        // course bit 2
	Number("([01]),").        // course bit 3
	Or().
	Number("(d+),").          // course
	Number("(?:d+,){3}").     // reserved
	Number("[01],").          // over speed start
	Number("[01],").          // over speed end
	Number("(?:d+,){3}").     // reserved
	Number("([01]),").        // power status
	Number("(?:d+,){2}").     // reserved
	Number("[01],").          // ignition on event
	Number("([01]),").        // ignition
	Number("[01],").          // ignition off event
	Number("(?:d+,){5}").     // reserved
	Number("[01],").          // low battery
	Number("[01],").          // corner packet
	Number("(?:d+,){6}").     // reserved
	Number("[01],").          // hard acceleration
	Number("[01],").          // hard braking
	Number("[01],[01],[01],[01],"). // course bits
	Number("(d+),").          // external voltage
	Number("(d+),").          // internal voltage
	Number("(?:d+,){6}").     // reserved
	Expression("P([^,]+),").  // obd
	Expression("D([^,]+),").  // dtcs
	Number("-?d+,").          // accelerometer x
	Number("-?d+,").          // accelerometer y
	Number("-?d+,").          // accelerometer z
	Number("d+,").            // delta distance
	Or().
	Number("(d+),").          // course
	Number("(d+),").          // satellites
	Number("(d+.d+),").       // hdop
	Number("(?:d+,){2}").     // reserved
	Number("(d+),").          // adc 1
	Number("([01]),").        // di 1
	Number("[01],").          // case open
	Number("[01],").          // over speed start
	Number("[01],").          // over speed end
	Number("(?:[01],){2}").   // reserved
	Number("[01],").          // immobilizer
	Number("([01]),").        // power status
	Number("([01]),").        // di 2
	Number("(?:[01],){2}").   // reserved
	Number("([01]),").        // ignition
	Number("(?:[01],){6}").   // reserved
	Number("[01],").          // low battery
	Number("[01],").          // corner packet
	Number("(?:[01],){4}").   // reserved
	Number("[01],").          // do 1
	Number("[01],").          // reserved
	Number("[01],").          // hard acceleration
	Number("[01],").          // hard braking
	Number("(?:[01],){4}").   // reserved
	Number("(d+),").          // external voltage
	Number("(d+),").          // internal voltage
	GroupEnd().
	Or().
	Number("(d+),").          // sensor id
	Expression("([^,]+),").   // sensor data
	GroupEnd().
	Text("*").
	Number("xx").             // checksum
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

	pos.Set(model.KeyEvent, parser.NextIntDefault(0))
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"

	if parser.HasNextN(3) {
		pos.Set(model.KeyRssi, parser.NextIntDefault(0))
		pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
	}

	switch {
	case parser.HasNextN(9):
		pos.Set(model.KeyFuel, parser.NextInt())
		pos.Set(model.PrefixIn+"1", parser.Next())
		pos.Set(model.KeyCharge, parser.Next() == "1")
		pos.Set(model.PrefixIn+"2", parser.Next())
		pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)
		course := parser.NextIntDefault(0) << 3
		course += parser.NextIntDefault(0) << 2
		course += parser.NextIntDefault(0) << 1
		course += parser.NextIntDefault(0)
		if course > 0 && course <= 8 {
			pos.Course = float64((course - 1) * 45)
		}
	case parser.HasNextN(7):
		pos.Course = float64(parser.NextIntDefault(0))
		pos.Set(model.KeyCharge, parser.Next() == "1")
		pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)
		pos.Set(model.KeyPower, parser.NextIntDefault(0))
		pos.Set(model.KeyBattery, parser.NextIntDefault(0))

		obd := parser.Next()
		pos.Set("obd", kit.Substring(obd, 1, kit.Len(obd)-1))

		dtcs := parser.Next()
		pos.Set(model.KeyDtcs, strings.ReplaceAll(kit.Substring(dtcs, 1, kit.Len(dtcs)-1), "|", " "))
	case parser.HasNextN(10):
		pos.Course = float64(parser.NextIntDefault(0))
		pos.Set(model.KeySatellites, parser.NextIntDefault(0))
		pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))
		pos.Set(model.PrefixAdc+"1", parser.NextIntDefault(0))
		pos.Set(model.PrefixIn+"1", parser.NextIntDefault(0))
		pos.Set(model.KeyCharge, parser.Next() == "1")
		pos.Set(model.PrefixIn+"2", parser.NextIntDefault(0))
		pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)
		pos.Set(model.KeyPower, parser.NextIntDefault(0))
		pos.Set(model.KeyBattery, parser.NextIntDefault(0))
	case parser.HasNextN(2):
		pos.Set("sensorId", parser.NextInt())
		pos.Set("sensorData", parser.Next())
	}
	return pos
}
