// Package jido is the Jido protocol (JidoProtocol): "*imei,command,..." text messages
// ending with '#'.
package jido

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "jido",
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
	Text("*").
	Number("(d+),").                  // imei
	Number("(d+),").                  // command
	Expression("([AV]),").Optional(). // validity
	Number("(dd)(dd)(dd),").          // date (ddmmyy)
	Number("(dd)(dd)(dd),").          // time (hhmmss)
	Number("(d+)(dd.d+),").           // latitude
	Expression("([NS]),").            //
	Number("(d+)(dd.d+),").           // longitude
	Expression("([EW]),").            //
	GroupBegin().
	Number("(d+),").       // speed
	Number("(d+),").       // odometer
	Number("(d+),").       // course
	Number("(-?d+),").     // altitude
	Number("(d+),").       // satellites
	Number("d+,").         // gsm 1
	Number("d+,").         // gsm 2
	Number("([01]),").     // charging
	Number("(d+),").       // battery level
	Expression("([YKN]),"). // mode
	Number("([01]),").     // lock
	Number("[^,]+,").      // accelerometer x
	Number("[^,]+,").      // accelerometer y
	Number("[^,]+,").      // accelerometer z
	Or().
	Expression("[^,]*,"). // data
	GroupEnd().
	Number("xx"). // checksum
	Compile()

func decodeAlarm(alarmType int) string {
	switch alarmType {
	case 3:
		return model.AlarmLowBattery
	case 4:
		return model.AlarmTampering
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

	pos.AddAlarm(decodeAlarm(parser.MustInt()))

	if parser.HasNext() {
		pos.Valid = parser.Next() == "A"
	} else {
		pos.Valid = true
	}

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	if parser.HasNextN(9) {
		pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
		pos.Set(model.KeyOdometer, parser.NextInt())
		pos.Course = float64(parser.MustInt())
		pos.Altitude = float64(parser.MustInt())
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyCharge, parser.MustInt() > 0)
		pos.Set(model.KeyBatteryLevel, parser.NextInt())
		pos.Set("mode", parser.Next())
		pos.Set(model.KeyBlocked, parser.MustInt() > 0)
	}

	return pos
}
