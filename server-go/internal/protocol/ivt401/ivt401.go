// Package ivt401 is the iVT401 protocol (Ivt401Protocol): "(TL?,...)" text reports
// ending with ';'.
package ivt401

import (
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "ivt401",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ";"),
				Text:    true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("(").
	Expression("TL[ABLN],"). // header
	Number("(d+),").         // imei
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("([-+]d+.d+),").  // latitude
	Number("([-+]d+.d+),").  // longitude
	Number("(d+),").         // speed
	Number("(d+),").         // course
	Number("(-?d+.?d*),").   // altitude
	Number("d+,").           // satellites or battery status
	Number("(d),").          // gps status
	Number("(d+),").         // rssi
	Number("(d+),").         // input
	Number("(d+),").         // output
	Number("(d+.d+),").      // adc
	Number("(d+.d+),").      // power
	Number("(d+.d+),").      // battery
	Number("(-?d+.?d*),").   // pcb temp
	Expression("([^,]+),").  // temp
	Number("(d+),").         // movement
	Number("(d+.d+),").      // acceleration
	Number("(-?d+),").       // tilt
	Number("(d+),").         // trip
	Number("(d+),").         // odometer
	GroupBegin().
	Number("([01]),").        // overspeed
	Number("[01],").          // input 2 misuse
	Number("[01],").          // immobilizer
	Number("[01],").          // temperature alert
	Number("[0-2]+,").        // geofence
	Number("([0-3]),").       // harsh driving
	Number("[01],").          // reconnect
	Number("([01]),").        // low battery
	Number("([01]),").        // power disconnected
	Number("[01],").          // gps failure
	Number("([01]),").        // towing
	Number("[01],").          // server unreachable
	Number("[128],").         // sleep mode
	Expression("([^,]+)?,").  // driver id
	Number("d+,").            // sms count
	GroupEndWith("?").
	Any().
	Compile()

// numericValue is Character.getNumericValue for ASCII chars.
func numericValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return -1
}


func alarmIf(pos *model.Position, cond bool, alarm string) {
	if cond {
		pos.AddAlarm(alarm)
	}
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

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = parser.MustDouble()
	pos.Valid = parser.MustInt() > 0

	pos.Set(model.KeyRssi, parser.NextInt())

	input := parser.Next()
	for i := 0; i < kit.Len(input); i++ {
		value := numericValue(kit.CharAt(input, i))
		if value < 2 {
			pos.Set(model.PrefixIn+strconv.Itoa(i+1), value > 0)
		}
	}

	output := parser.Next()
	for i := 0; i < kit.Len(output); i++ {
		pos.Set(model.PrefixOut+strconv.Itoa(i+1), numericValue(kit.CharAt(output, i)) > 0)
	}

	pos.Set(model.PrefixAdc+"1", parser.NextDouble())
	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyDeviceTemp, parser.NextDouble())

	temp := parser.Next()
	if strings.HasPrefix(temp, "M") {
		index := 1
		start := 1
		for start < kit.Len(temp) {
			end := kit.IndexOfFrom(temp, "-", start+1)
			if end < 0 {
				end = kit.IndexOfFrom(temp, "+", start+1)
			}
			if end < 0 {
				end = kit.Len(temp)
			}
			if end > 0 {
				pos.Set(model.PrefixTemp+strconv.Itoa(index), kit.Double(kit.Substring(temp, start, end)))
				index++
			}
			start = end
		}
	} else {
		pos.Set(model.PrefixTemp+"1", kit.Double(temp))
	}

	pos.Set(model.KeyMotion, parser.MustInt() > 0)
	pos.Set(model.KeyAcceleration, parser.NextDouble())

	parser.NextInt() // tilt
	parser.NextInt() // trip state

	pos.Set(model.KeyOdometer, parser.NextLong())

	if parser.HasNextN(6) {
		alarmIf(pos, parser.MustInt() == 1, model.AlarmOverspeed)
		switch parser.MustInt() {
		case 1:
			pos.AddAlarm(model.AlarmAcceleration)
		case 2:
			pos.AddAlarm(model.AlarmBraking)
		case 3:
			pos.AddAlarm(model.AlarmCornering)
		}
		alarmIf(pos, parser.MustInt() == 1, model.AlarmLowBattery)
		alarmIf(pos, parser.MustInt() == 1, model.AlarmPowerCut)
		alarmIf(pos, parser.MustInt() == 1, model.AlarmTow)
		pos.Set(model.KeyDriverUniqueID, parser.Next())
	}

	return pos
}
