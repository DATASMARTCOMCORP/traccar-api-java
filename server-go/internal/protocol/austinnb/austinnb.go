// Package austinnb is the Austin NB protocol (AustinNbProtocol): ';'-separated text
// datagrams over UDP.
package austinnb

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "austinnb",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("(d+);").              // imei
	Number("(dddd)-(dd)-(dd) ").  // date
	Number("(dd):(dd):(dd);").    // time
	Number("(-?d+,d+);").         // latitude
	Number("(-?d+,d+);").         // longitude
	Number("(d+);").              // azimuth
	Number("(d+);").              // angle
	Number("(d+);").              // range
	Number("(d+);").              // out of range
	Expression("(.*)").           // operator
	Any().
	Compile()

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

	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = true
	pos.SetLatitude(kit.Double(strings.ReplaceAll(parser.Next(), ",", ".")))
	pos.SetLongitude(kit.Double(strings.ReplaceAll(parser.Next(), ",", ".")))
	pos.Course = float64(parser.MustInt())

	pos.Set("angle", parser.NextInt())
	pos.Set("range", parser.NextInt())
	pos.Set("outOfRange", parser.NextInt())
	pos.Set("carrier", parser.Next())
	return pos
}
