// Package c2stek is the C2stek protocol (C2stekProtocol): "PA$...$AP" text reports over
// TCP.
package c2stek

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
		Name: "c2stek",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			framer := ingest.NewDelimiterFramer(ingest.MaxFrameLength, "$AP")
			framer.KeepEnding = true
			return ingest.Pipeline{
				Framer:  framer,
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("PA$").
	Number("(d+)"). // imei
	Text("$").
	Expression(".#").        // data type
	Number("(dd)(dd)(dd)#"). // date (yymmdd)
	Number("(dd)(dd)(dd)#"). // time (hhmmss)
	Number("([01])#").       // valid
	Number("([+-]?d+.d+)#"). // latitude
	Number("([+-]?d+.d+)#"). // longitude
	Number("(d+.d+)#").      // speed
	Number("(d+.d+)#").      // course
	Number("(-?d+.d+)#").    // altitude
	Number("(d+)#").         // battery
	Number("d+#").           // geo area alarm
	Number("(x+)").          // alarm
	GroupBegin().
	Number("#([01])?"). // armed
	Number("([01])").   // door
	Number("([01])#").  // ignition
	GroupEndWith("?").
	Any().
	Text("$AP").
	Compile()

func decodeAlarm(alarm int) string {
	switch alarm {
	case 0x2:
		return model.AlarmVibration
	case 0x3:
		return model.AlarmPowerCut
	case 0x4:
		return model.AlarmOverspeed
	case 0x5:
		return model.AlarmSos
	case 0x6:
		return model.AlarmDoor
	case 0xA:
		return model.AlarmLowBattery
	case 0xB:
		return model.AlarmFault
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	if strings.Contains(sentence, "$20$") {
		m.Send(sentence)
	}

	parser := kit.NewParser(pattern, sentence)
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
	pos.Valid = parser.MustInt() > 0
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()
	pos.Altitude = parser.MustDouble()

	pos.Set(model.KeyBattery, float64(parser.MustInt())/1000.0)
	pos.AddAlarm(decodeAlarm(parser.MustHexInt()))

	if parser.HasNext() {
		pos.Set(model.KeyArmed, parser.MustInt() > 0)
	}
	if parser.HasNextN(2) {
		pos.Set(model.KeyDoor, parser.MustInt() > 0)
		pos.Set(model.KeyIgnition, parser.MustInt() > 0)
	}
	return pos
}
