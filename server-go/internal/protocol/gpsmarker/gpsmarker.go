// Package gpsmarker is the GpsMarker protocol (GpsMarkerProtocol): "$GM" text messages
// ending with '\r'.
package gpsmarker

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gpsmarker",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\r"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("$GM").
	Number("d").               // type
	Number("(?:xx)?").         // index
	Number("(d{15})").         // imei
	Number("T(dd)(dd)(dd)").   // date (ddmmyy)
	Number("(dd)(dd)(dd)?").   // time (hhmmss)
	Expression("([NS])").      //
	Number("(dd)(dd)(dddd)").  // latitude
	Expression("([EW])").      //
	Number("(ddd)(dd)(dddd)"). // longitude
	Number("(ddd)").           // speed
	Number("(ddd)").           // course
	Number("(x)").             // satellites
	Number("(dd)").            // battery
	Number("(d)").             // input
	Number("(d)").             // output
	Number("(ddd)").           // temperature
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.Valid = true
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMinMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMinMin))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	pos.Set(model.KeySatellites, parser.NextHexIntDefault(0))
	pos.Set(model.KeyBatteryLevel, parser.NextIntDefault(0))
	pos.Set(model.KeyInput, parser.Next())
	pos.Set(model.KeyOutput, parser.Next())
	pos.Set(model.PrefixTemp+"1", parser.Next())
	return pos
}
