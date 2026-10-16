// Package gl100 is the GL100 protocol (Gl100Protocol): Queclink "+RESP:" text reports
// ending with '\0', over TCP and UDP.
package gl100

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gl100",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\x00"),
					Text:    true,
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

var pattern = kit.NewPattern().
	Text("+RESP:").
	Expression("GT...,").
	Number("(d{15}),"). // imei
	GroupBegin().
	Number("d+,"). // number
	Number("d,").  // reserved / geofence id
	Number("d+").  // reserved / geofence alert // battery
	Or().
	Number("[^,]*"). // calling number
	GroupEndWith(",").
	Expression("([01]),").       // gps fix
	Number("(d+.d),").           // speed
	Number("(d+),").             // course
	Number("(-?d+.d),").         // altitude
	Number("d*,").               // gps accuracy
	Number("(-?d+.d+),").        // longitude
	Number("(-?d+.d+),").        // latitude
	Number("(dddd)(dd)(dd)").    // date (yyyymmdd)
	Number("(dd)(dd)(dd),").     // time (hhmmss)
	Any().
	Compile()

// lastIndexOf is String.lastIndexOf in UTF-16 chars.
func lastIndexOf(s, sub string) int {
	i := strings.LastIndex(s, sub)
	if i <= 0 {
		return i
	}
	return kit.Len(s[:i])
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if strings.Contains(sentence, "AT+GTHBD=") && m.HasChannel() {
		response := "+RESP:GTHBD,GPRS ACTIVE,"
		response += kit.Substring(sentence, 9, lastIndexOf(sentence, ","))
		response += "\x00"
		m.Send(response) // heartbeat response
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Valid = parser.NextIntDefault(0) == 0
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.SetLatitude(parser.NextDoubleDefault(0))

	pos.SetTime(parser.NextDateTimeDefault())

	return pos
}
