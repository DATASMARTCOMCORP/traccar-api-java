// Package envotech is the Envotech protocol (EnvotechProtocol): '#'-delimited text
// messages over TCP.
package envotech

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "envotech",
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
	Text("$").
	Number("dd").               // mode
	Expression("...,").         // hardware
	Number("(x+),").            // event
	Number("x+,").              // group
	Number("(x+),").            // device id
	Number("(dd)(dd)(dd)").     // date (ddmmyy)
	Number("(dd)(dd)(dd),").    // time (hhmmss)
	Number("xx").               // connection status
	Number("(dd)").             // rssi
	Number("d{5},").            // mcc
	Number("(ddd)").            // power
	Number("(ddd),").           // battery
	Number("(xx)").             // inputs
	Number("(xx),").            // outputs
	Number("(xxx)?").           // fuel
	Number("(xxx)?,").          // weight
	Number("(x{8}),").          // status
	Expression("[^']*'").       //
	Number("(dd)(dd)(dd)").     // date (ddmmyy)
	Number("(dd)(dd)(dd)").     // time (hhmmss)
	Number("(d)").              // fix
	Number("(d+)(d{5})([NS])"). // latitude
	Number("(d+)(d{5})([EW])"). // longitude
	Number("(ddd)").            // speed
	Number("(ddd)").            // course
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	event := parser.MustHexInt()
	switch event {
	case 0x60:
		pos.AddAlarm(model.AlarmLock)
	case 0x61:
		pos.AddAlarm(model.AlarmUnlock)
	}
	pos.Set(model.KeyEvent, event)

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.DeviceTime = model.NewTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeyPower, float64(parser.MustInt())/100.0)
	pos.Set(model.KeyBattery, float64(parser.MustInt())/100.0)
	pos.Set(model.KeyInput, parser.NextHexInt())
	pos.Set(model.PrefixOut, parser.NextHexInt())
	pos.Set(model.KeyFuel, parser.NextHexInt())
	pos.Set("weight", parser.NextHexInt())
	pos.Set(model.KeyStatus, parser.NextHexLong())

	pos.FixTime = model.NewTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.Valid = parser.MustInt() > 0
	pos.SetLatitude(parser.NextCoordinate(kit.DegDegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegDegHem))
	pos.Speed = float64(parser.MustInt())
	pos.Course = float64(parser.MustInt())
	return pos
}
