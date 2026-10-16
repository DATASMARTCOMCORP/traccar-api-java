// Package arknavx8 is the Arknav X8 protocol (ArknavX8Protocol): ';'-terminated text
// messages over TCP, a login with the IMEI, then 1G/1R/1M fixes and 2G status reports.
package arknavx8

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "arknavx8",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ";"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern1G = kit.NewPattern().
	Expression("(..),").          // type
	Number("(dd)(dd)(dd)").       // date (yymmdd)
	Number("(dd)(dd)(dd),").      // time (hhmmss)
	Expression("([AV]),").        // validity
	Number("(d+)(dd.d+)([NS]),"). // latitude
	Number("(d+)(dd.d+)([EW]),"). // longitude
	Number("(d+.d+),").           // speed
	Number("(d+),").              // course
	Number("(d+.d+),").           // hdop
	Number("(d+)").               // status
	Compile()

var pattern2G = kit.NewPattern().
	Expression("..,").            // type
	Number("(dd)(dd)(dd)").       // date (yymmdd)
	Number("(dd)(dd)(dd),").      // time (hhmmss)
	Number("(d+),").              // satellites
	Number("(d+.d+),").           // altitude
	Number("(d+.d+),").           // power
	Number("(d+.d+),").           // battery
	Number("(d+.d+)").            // odometer
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	if kit.CharAt(sentence, 2) != ',' {
		m.Session(kit.Substring(sentence, 0, 15))
		return nil
	}
	switch kit.Substring(sentence, 0, 2) {
	case "1G", "1R", "1M":
		return decode1G(m, sentence)
	case "2G":
		return decode2G(m, sentence)
	}
	return nil
}

func decode1G(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern1G, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyType, parser.Next())
	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))
	pos.Set(model.KeyStatus, parser.Next())
	return pos
}

func decode2G(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern2G, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session()
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := parser.NextDateTimeDefault()
	m.LastLocation(pos, &t)

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Altitude = parser.MustDouble()
	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyOdometer, parser.MustDouble()*1852/3600)
	return pos
}
