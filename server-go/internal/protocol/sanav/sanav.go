// Package sanav is the Sanav protocol (SanavProtocol): "imei=...&rmc=$GPRMC,..." text
// messages over TCP and UDP.
package sanav

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	pipeline := func() ingest.Pipeline {
		return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
	}
	return ingest.Protocol{
		Name:    "sanav",
		Servers: []ingest.Server{{NewPipeline: pipeline}, {Datagram: true, NewPipeline: pipeline}},
	}
}

var pattern = kit.NewPattern().
	Expression("imei[:=]").
	Number("(d+)"). // imei
	Expression("&?rmc[:=]").
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).d+,"). // time (hhmmss.sss)
	Expression("([AV]),").      // validity
	Number("(d+)(dd.d+),").     // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+),").      // speed
	Number("(d+.d+)?,").     // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	GroupBegin().
	Expression("[^*]*").
	Text("*").
	Number("xx,").
	Expression("[^,]+,"). // status
	Number("(d+),").      // io
	GroupEndWith("?").
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

	dateBuilder := kit.NewDateBuilder(nil).
		SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.MustDouble()
	pos.Course = parser.NextDoubleDefault(0)

	dateBuilder.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(dateBuilder.Date())

	if parser.HasNext() {
		io := int64(parser.MustHexInt())
		for i := range 5 {
			pos.Set(model.PrefixIn+strconv.Itoa(i+1), kit.Check(io, i))
		}
		pos.Set(model.KeyIgnition, kit.Check(io, 5))
		pos.Set(model.PrefixOut+"1", kit.Check(io, 6))
		pos.Set(model.PrefixOut+"2", kit.Check(io, 7))
		pos.Set(model.KeyCharge, kit.Check(io, 8))
		if !kit.Check(io, 9) {
			pos.AddAlarm(model.AlarmLowBattery)
		}
	}

	return pos
}
