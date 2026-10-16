// Package gpsmta is the Gpsmta protocol (GpsmtaProtocol): space-separated text messages
// over UDP, acknowledged with their time.
package gpsmta

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gpsmta",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Expression("([^ ]+) "). // uid
	Number("(d+) ").        // time (unix time)
	Number("(-?d+.d+) ").   // latitude
	Number("(-?d+.d+) ").   // longitude
	Number("(d+) ").        // speed
	Number("(d+) ").        // course
	Number("(d+) ").        // accuracy
	Number("(d+) ").        // altitude
	Number("(d+) ").        // flags
	Number("(d+) ").        // battery
	Number("(d+) ").        // temperature
	Number("(d)").          // charging status
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

	t := parser.Next()
	pos.SetTime(time.UnixMilli(kit.Long(t) * 1000))
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = float64(parser.MustInt())
	pos.Course = float64(parser.MustInt())
	pos.Accuracy = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())
	pos.Set(model.KeyStatus, parser.NextInt())
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.PrefixTemp+"1", parser.NextInt())
	pos.Set(model.KeyCharge, parser.MustInt() == 1)

	m.Send(t)
	return pos
}
