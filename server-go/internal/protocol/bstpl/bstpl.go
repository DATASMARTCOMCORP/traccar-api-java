// Package bstpl is the BSTPL protocol (BstplProtocol): "BSTPL$" text reports ending with
// '#' over TCP.
package bstpl

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "bstpl",
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
	Text("BSTPL$").             // header
	Number("(d),").             // type
	Expression("([^,]+),").     // device id
	Expression("([AV]),").      // validity
	Number("(dd)(dd)(dd),").    // date (ddmmyy)
	Number("(dd)(dd)(dd),").    // time (hhmmss)
	Number("(d+.d+),([0NS]),"). // latitude
	Number("(d+.d+),([0EW]),"). // longitude
	Number("(d+),").            // speed
	Number("(d+),").            // odometer
	Number("(d+),").            // course
	Number("(d+),").            // satellites
	GroupBegin().
	Number("([01]),").      // box open
	Number("(d+),").        // rssi
	Number("([01]),").      // charge
	Number("([01]),").      // ignition
	Number("([01]),").      // engine
	Number("([01]),").      // locked
	Number("(d+.d+),").     // adc
	Number("d+,").          // reserved
	Number("(d+.d+),").     // battery
	Expression("([^,]+),"). // firmware
	Number("([^,]+),").     // iccid
	Number("(d+.d+)").      // power
	Or().
	Number("(d+),").        // rssi
	Number("([01]),").      // box open
	Number("(d+.d+),").     // battery
	Number("([01]),").      // charge
	Number("([01]),").      // ignition
	Number("(d+.d+),").     // power
	Number("(d+),").        // altitude
	Number("[01],").        // reserved
	Expression("([^,]+),"). // firmware
	Number("([^,]+),").     // iccid
	Text("{").
	Number("(d+),").    // rpm
	Number("(d+),").    // can odometer
	Number("(d+),").    // fuel
	Number("(d+),").    // gear
	Number("(d+.d+),"). // engine temperature
	Number("(d+.d+)").  // coolant temperature
	Text("}").
	GroupEnd().
	Compile()

func decodeAlarm(value int) string {
	switch value {
	case 4:
		return model.AlarmLowBattery
	case 5:
		return model.AlarmAcceleration
	case 6:
		return model.AlarmBraking
	case 7:
		return model.AlarmOverspeed
	case 9:
		return model.AlarmSos
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	typ := parser.MustInt()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.AddAlarm(decodeAlarm(typ))

	pos.Valid = parser.Next() == "A"
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))

	pos.Set(model.KeyOdometer, int64(parser.MustInt())*1000)

	pos.Course = float64(parser.MustInt())

	pos.Set(model.KeySatellites, parser.NextInt())

	if parser.HasNextN(11) {
		boxOpen := parser.MustInt() > 0
		if typ == 8 && boxOpen {
			pos.AddAlarm(model.AlarmTampering)
		}
		pos.Set("boxOpen", boxOpen)

		pos.Set(model.KeyRssi, parser.NextInt())

		charge := parser.MustInt() > 0
		if typ == 3 {
			if charge {
				pos.AddAlarm(model.AlarmPowerRestored)
			} else {
				pos.AddAlarm(model.AlarmPowerCut)
			}
		}
		pos.Set(model.KeyCharge, charge)

		pos.Set(model.KeyIgnition, parser.MustInt() > 0)
		pos.Set("engine", parser.MustInt() > 0)
		pos.Set(model.KeyBlocked, parser.MustInt() > 0)
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.KeyBattery, parser.NextDouble())
		pos.Set(model.KeyVersionFw, parser.Next())
		pos.Set(model.KeyIccid, parser.Next())
		pos.Set(model.KeyPower, parser.NextDouble())
	} else {
		pos.Set(model.KeyRssi, parser.NextInt())
		pos.Set("boxOpen", parser.MustInt() > 0)
		pos.Set(model.KeyBattery, parser.NextDouble())
		pos.Set(model.KeyCharge, parser.MustInt() > 0)
		pos.Set(model.KeyIgnition, parser.MustInt() > 0)
		pos.Set(model.KeyPower, parser.NextDouble())
		pos.Altitude = float64(parser.MustInt())
		pos.Set(model.KeyVersionFw, parser.Next())
		pos.Set(model.KeyIccid, parser.Next())

		pos.Set(model.KeyRpm, parser.NextInt())
		pos.Set(model.KeyObdOdometer, parser.NextInt())
		pos.Set(model.KeyFuel, parser.NextInt())
		pos.Set("gear", parser.NextInt())
		pos.Set(model.KeyEngineTemp, parser.NextDouble())
		pos.Set(model.KeyCoolantTemp, parser.NextDouble())
	}
	return pos
}
