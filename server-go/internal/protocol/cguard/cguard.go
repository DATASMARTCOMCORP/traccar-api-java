// Package cguard is the Cguard protocol (CguardProtocol): text lines (ID, NV positions,
// BC status) over TCP.
package cguard

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
		Name: "cguard",
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

var patternNV = kit.NewPattern().
	Text("NV:").
	Number("(dd)(dd)(dd) ").                // date (yymmdd)
	Number("(dd)(dd)(dd)").                 // time (hhmmss)
	Number(":(-?d+.d+)").                   // longitude
	Number(":(-?d+.d+)").                   // latitude
	Number(":(d+.?d*)").                    // speed
	Number(":(?:NAN|(d+.?d*))").            // accuracy
	Number(":(?:NAN|(d+.?d*))").            // course
	Number(":(?:NAN|(d+.?d*))").Optional(). // altitude
	Compile()

var patternBC = kit.NewPattern().
	Text("BC:").
	Number("(dd)(dd)(dd) "). // date (yymmdd)
	Number("(dd)(dd)(dd):"). // time (hhmmss)
	Expression("(.+)").      // data
	Compile()

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	parser := kit.NewParser(patternNV, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTimeDefault())

	pos.Valid = true
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))

	pos.Accuracy = parser.NextDoubleDefault(0)

	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)
	return pos
}

func decodeStatus(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	parser := kit.NewParser(patternBC, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := parser.NextDateTimeDefault()
	m.LastLocation(pos, &t)

	data := kit.Split(parser.Next(), ":")
	for i := 0; i < len(data)/2; i++ {
		key, value := data[i*2], data[i*2+1]
		switch key {
		case "CSQ1":
			pos.Set(model.KeyRssi, kit.Int(value))
		case "NSQ1":
			pos.Set(model.KeySatellites, kit.Int(value))
		case "BAT1":
			if strings.Contains(value, ".") {
				pos.Set(model.KeyBattery, kit.Double(value))
			} else {
				pos.Set(model.KeyBatteryLevel, kit.Int(value))
			}
		case "PWR1":
			pos.Set(model.KeyPower, kit.Double(value))
		default:
			pos.Set(strings.ToLower(key), value)
		}
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if strings.HasPrefix(sentence, "ID:") || strings.HasPrefix(sentence, "IDRO:") {
		m.Session(kit.SubstringFrom(sentence, kit.IndexOf(sentence, ":")+1))
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	var pos *model.Position
	switch {
	case strings.HasPrefix(sentence, "NV:"):
		pos = decodePosition(m, ds, sentence)
	case strings.HasPrefix(sentence, "BC:"):
		pos = decodeStatus(m, ds, sentence)
	}
	if pos == nil {
		return nil
	}
	return pos
}
