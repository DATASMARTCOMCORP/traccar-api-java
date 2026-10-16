// Package trackbox is the Trackbox protocol (TrackboxProtocol): an "a=connect" login
// line, then NMEA-like text lines, each acknowledged with "=OK=".
package trackbox

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "trackbox",
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

var pattern = kit.NewPattern().
	Number("(dd)(dd)(dd).(ddd),").   // time (hhmmss.sss)
	Number("(dd)(dd.dddd)([NS]),").  // latitude
	Number("(ddd)(dd.dddd)([EW]),"). // longitude
	Number("(d+.d),").               // hdop
	Number("(-?d+.?d*),").           // altitude
	Number("(d),").                  // fix type
	Number("(d+.d+),").              // course
	Number("d+.d+,").                // speed (kph)
	Number("(d+.d+),").              // speed (knots)
	Number("(dd)(dd)(dd),").         // date (ddmmyy)
	Number("(d+)").                  // satellites
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if strings.HasPrefix(sentence, "a=connect") {
		id := kit.SubstringFrom(sentence, kit.IndexOf(sentence, "i=")+2)
		if m.Session(id) != nil {
			m.Send("=OK=\r\n")
		}
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	m.Send("=OK=\r\n")

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	db := kit.NewDateBuilder(nil).SetTimeMillis(
		parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	pos.Set(model.KeyHdop, parser.NextDouble())

	pos.Altitude = parser.NextDoubleDefault(0)

	fix := parser.NextIntDefault(0)
	pos.Set(model.KeyGps, fix)
	pos.Valid = fix > 0

	pos.Course = parser.NextDoubleDefault(0)
	pos.Speed = parser.NextDoubleDefault(0)

	db.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(db.Date())

	pos.Set(model.KeySatellites, parser.NextInt())

	return pos
}
