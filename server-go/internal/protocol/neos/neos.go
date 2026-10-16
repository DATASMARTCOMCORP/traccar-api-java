// Package neos is the Neos protocol (NeosProtocol): ">"-prefixed text datagrams, each
// acknowledged with "$OK!".
package neos

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "neos",
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
	Text(">").
	Number("(d{8}),").       // id
	Number("d+,").           // status
	Number("([01]),").       // valid
	Number("(dd)(dd)(dd),"). // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([EW])").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([NS])").
	Number("(d+)(dd.d+),"). // latitude
	Expression("[^,]*,").   // response
	Number("(d+),").        // speed
	Number("(d+),").        // course
	Number("(d+),").        // rssi
	Expression("[^,]*,").   // event data
	Number("(d+)-").        // adc
	Number("(d+),").        // battery
	Number("0,").
	Number("d,").
	Number("([01]{8})"). // input
	Text("*").
	Number("xx!").
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	m.Send("$OK!")

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

	pos.Valid = parser.MustInt() > 0
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = float64(parser.MustInt())
	pos.Course = float64(parser.MustInt())

	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.NextInt())
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.KeyInput, parser.NextBinInt())

	return pos
}
