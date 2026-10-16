// Package v680 is the V680 protocol (V680Protocol): '#'-separated text messages ending
// with "##" over TCP, and over UDP.
package v680

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "v680",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "##"),
					Text:    true,
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Text:    true,
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
		},
	}
}

var pattern = kit.NewPattern().
	GroupBegin().
	Number("#(d+)#").       // imei
	Expression("([^#]*)#"). // user
	GroupEndWith("?").
	Number("(d+)#").          // fix
	Expression("([^#]+)#").   // password
	Expression("([^#]+)#").   // event
	Number("(d+)#").          // packet number
	Expression("([^#]+)?#?"). // gsm base station
	Expression("(?:[^#]+#)?").
	Number("(d+.d+),([EW]),"). // longitude
	Number("(d+.d+),([NS]),"). // latitude
	Number("(d+.d+),").        // speed
	Number("(d+.?d*)?#").      // course
	Number("(dd)(dd)(dd)#").   // date (ddmmyy)
	Number("(dd)(dd)(dd)").    // time (hhmmss)
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))

	if kit.Len(sentence) == 16 {
		m.Session(kit.SubstringFrom(sentence, 1))
		return nil
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	var ds *ingest.DeviceSession
	if parser.HasNext() {
		ds = m.Session(parser.Next())
	} else {
		ds = m.Session()
	}
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set("user", parser.NextNullable())
	pos.Valid = parser.NextIntDefault(0) > 0
	pos.Set("password", parser.NextNullable())
	pos.Set(model.KeyEvent, parser.NextNullable())
	pos.Set("packet", parser.NextNullable())
	pos.Set("lbsData", parser.NextNullable())

	lon := parser.NextDoubleDefault(0)
	west := parser.Next() == "W"
	lat := parser.NextDoubleDefault(0)
	south := parser.Next() == "S"

	if lat > 90 || lon > 180 {
		lonDegrees := int(lon / 100.0)
		lon = (lon - float64(lonDegrees*100)) / 60.0
		lon += float64(lonDegrees)

		latDegrees := int(lat / 100.0)
		lat = (lat - float64(latDegrees*100)) / 60.0
		lat += float64(latDegrees)
	}

	if west {
		lon = -lon
	}
	pos.SetLongitude(lon)
	if south {
		lat = -lat
	}
	pos.SetLatitude(lat)

	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	day := parser.NextIntDefault(0)
	month := parser.NextIntDefault(0)
	if day == 0 && month == 0 {
		return nil // invalid date
	}

	b := kit.NewDateBuilder(nil)
	b.SetDate(parser.NextIntDefault(0), month, day)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(b.Date())

	return pos
}
