// Package techtlt is the TechTLT protocol (TechTltProtocol): text status and position
// messages over UDP.
package techtlt

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "techtlt",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var patternStatus = kit.NewPattern().
	Number("(d+),"). // id
	Text("INFOGPRS,").
	Number("V Bat=(d+.d),"). // battery
	Number("TEMP=(d+),").    // temperature
	Expression("[^,]*,").
	Number("(d+)"). // rssi
	Compile()

var patternPosition = kit.NewPattern().
	Number("(d+)"). // id
	Text("*POS=Y,").
	Number("(dd):(dd):(dd),"). // time
	Number("(dd)/(dd)/(dd),"). // date
	Number("(dd)(dd.d+)").     // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+)"). // longitude
	Expression("([EW]),").
	Number("(d+.d+),"). // speed
	Number("(d+.d+),"). // course
	Number("(d+.d+),"). // altitude
	Number("(d+),").    // satellites
	Number("(d+),").    // lac
	Number("(d+)").     // cid
	Compile()

func decodeStatus(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternStatus, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyDeviceTemp, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())
	return pos
}

func decodeLocation(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternPosition, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Valid = true
	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()
	pos.Altitude = parser.MustDouble()

	pos.Set(model.KeySatellites, parser.NextInt())

	lac := parser.MustInt()
	cid := parser.MustInt()
	pos.Network = model.NetworkOf(m.CellTowerFromLacCid(lac, int64(cid)))
	return pos
}

func decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))
	switch {
	case strings.Contains(sentence, "INFO"):
		return decodeStatus(m, sentence)
	case strings.Contains(sentence, "POS"):
		return decodeLocation(m, sentence)
	}
	return nil
}
