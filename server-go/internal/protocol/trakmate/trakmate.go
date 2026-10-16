// Package trakmate is the TrakMate protocol (TrakMateProtocol): "^TM..." text messages
// delimited by '#'.
//
// As in Java, the '|' of the expression and number parts of the patterns is regex
// alternation, not a literal; only text parts are quoted.
package trakmate

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "trakmate",
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

var patternSrt = kit.NewPattern().
	Text("^TMSRT|").
	Expression("([^ ]+)|").  // uid
	Number("(d+.d+)|").      // latitude
	Number("(d+.d+)|").      // longitude
	Number("(dd)(dd)(dd)|"). // time (hhmmss)
	Number("(dd)(dd)(dd)|"). // date (ddmmyy)
	Number("(d+.d+)|").      // software ver
	Number("(d+.d+)|").      // Hardware ver
	Any().
	Compile()

var patternPer = kit.NewPattern().
	Text("^TM").
	Expression("...|").         // type
	Expression("([^ ]+)|").     // uid
	Number("(d+)|").            // seq
	Number("(d+.d+)|").         // latitude
	Number("(d+.d+)|").         // longitude
	Number("(dd)(dd)(dd)|").    // time (hhmmss)
	Number("(dd)(dd)(dd)|").    // date (ddmmyy)
	Number("(d+.d+)|").         // speed
	Number("(d+.d+)|").         // heading
	Number("(d+)|").Optional(). // satellites
	Number("([01])|").          // ignition
	GroupBegin().
	Number("(d+)|").    // dop1
	Number("(d+)|").    // dop2
	Number("(d+.d+)|"). // analog
	Number("(d+.d+)|"). // internal battery
	Or().
	Number("-?d+ -?d+ -?d+|"). // accelerometer
	Number("([01])|").         // movement
	GroupEnd().
	Number("(d+.d+)|").            // vehicle battery
	Number("(d+.d+)|").            // gps odometer
	Number("(d+.d+)|").Optional(). // pulse odometer
	Number("([01])|").             // main power status
	Number("([01])|").             // gps data validity
	Number("([01])|").             // live or cache
	Any().
	Compile()

var patternAlt = kit.NewPattern().
	Text("^TMALT|").
	Expression("([^ ]+)|").  // uid
	Number("(d+)|").         // seq
	Number("(d+)|").         // Alert type
	Number("(d+)|").         // Alert status
	Number("(d+.d+)|").      // latitude
	Number("(d+.d+)|").      // longitude
	Number("(dd)(dd)(dd)|"). // time (hhmmss)
	Number("(dd)(dd)(dd)|"). // date (ddmmyy)
	Number("(d+.d+)|").      // speed
	Number("(d+.d+)|").      // heading
	Any().
	Compile()

func decodeAlarm(value int) string {
	switch value {
	case 1:
		return model.AlarmSos
	case 3:
		return model.AlarmGeofence
	case 4:
		return model.AlarmPowerCut
	}
	return ""
}

func decodeSrt(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternSrt, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))

	pos.Set(model.KeyVersionFw, parser.NextNullable())
	pos.Set(model.KeyVersionHw, parser.NextNullable())

	return pos
}

func decodeAlt(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternAlt, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	parser.Next() // seq
	pos.AddAlarm(decodeAlarm(parser.MustInt()))
	parser.Next() // alert status or data

	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))

	pos.Speed = parser.MustDouble()
	pos.Course = parser.MustDouble()

	return pos
}

func decodePer(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternPer, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	parser.Next() // seq

	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))

	pos.Speed = parser.MustDouble()
	pos.Course = parser.MustDouble()

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyIgnition, parser.MustInt() > 0)

	if parser.HasNextN(4) {
		pos.Set("dop1", parser.NextInt())
		pos.Set("dop2", parser.NextInt())
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.KeyBattery, parser.NextDouble())
	}

	if parser.HasNext() {
		pos.Set(model.KeyMotion, parser.NextIntDefault(0) > 0)
	}

	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyOdometer, parser.NextDouble())
	pos.Set("pulseOdometer", parser.NextDouble())
	pos.Set(model.KeyStatus, parser.NextInt())

	pos.Valid = parser.MustInt() > 0

	pos.Set(model.KeyArchive, parser.MustInt() > 0)

	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	typeIndex := kit.IndexOf(sentence, "^TM")
	if typeIndex < 0 {
		return nil
	}

	switch kit.Substring(sentence, typeIndex+3, typeIndex+6) {
	case "ALT":
		return decodeAlt(m, sentence)
	case "SRT":
		return decodeSrt(m, sentence)
	}
	return decodePer(m, sentence)
}
