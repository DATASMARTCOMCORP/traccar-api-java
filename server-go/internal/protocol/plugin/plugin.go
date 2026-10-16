// Package plugin is the Plugin protocol (PluginProtocol): comma-separated text reports
// ended by '#', each answered with a heartbeat.
package plugin

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "plugin",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "#"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Expression("[^0-9,]*,?").
	Number("([^,]+),").       // device id
	Number("(dddd)(dd)(dd)"). // date (yyyymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Number("(-?d+.d+),").     // longitude
	Number("(-?d+.d+),").     // latitude
	Number("(d+.?d*),").      // speed
	Number("(d+),").          // course
	Number("(-?d+),").        // altitude
	Number("(-?d+),").        // satellites
	Number("d+,").            // type
	Number("(d+.?d*),").      // odometer
	Number("(d+),").          // status
	Number("(d+.?d*),").      // fuel
	Expression("[^,]*,").
	Text("0").
	GroupBegin().
	Number(",(-?d+.?d*)"). // temperature 1
	Number(",(-?d+.?d*)"). // temperature 2
	Number(",d+").         // oil level
	Number(",(d+)").       // rpm
	Number(",(d+)").       // obd speed
	Number(",d+").         // people up
	Number(",d+").         // people down
	Number(",d+").         // obd status
	Number(",d+").         // fuel intake air temperature
	Number(",(d+)").       // throttle
	Number(",(d+)").       // battery
	GroupEndWith("?").
	GroupBegin().
	Text(",+,").
	Number("(d+),"). // event
	GroupEndWith("?").
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	m.Send("$$hb,1#")

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
	pos.SetLongitude(parser.MustDouble())
	pos.SetLatitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyOdometer, int64(parser.MustDouble()*1000))

	status := parser.MustLong()
	pos.Valid = kit.Check(status, 0)
	pos.Set(model.KeyIgnition, kit.Check(status, 1))
	for i := range 4 {
		pos.Set(model.PrefixIn+strconv.Itoa(i+1), kit.Check(status, 20+i))
	}
	pos.Set(model.KeyStatus, status)

	pos.Set(model.KeyFuel, parser.NextDouble())

	if parser.HasNextN(6) {
		pos.Set(model.PrefixTemp+"1", parser.NextDouble())
		pos.Set(model.PrefixTemp+"2", parser.NextDouble())
		pos.Set(model.KeyRpm, parser.NextInt())
		pos.Set(model.KeyObdSpeed, parser.NextInt())
		pos.Set(model.KeyThrottle, float64(parser.MustInt())/10.0)
		pos.Set(model.KeyPower, float64(parser.MustInt())/10.0)
	}

	if parser.HasNext() {
		event := parser.MustInt()
		switch event {
		case 11317:
			pos.AddAlarm(model.AlarmAcceleration)
		case 11319:
			pos.AddAlarm(model.AlarmBraking)
		}
		pos.Set(model.KeyEvent, event)
	}

	return pos
}
