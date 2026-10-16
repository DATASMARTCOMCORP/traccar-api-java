// Package r16h is the R16H protocol (R16hProtocol): "@GPSD", "@LBSD" and "@LINK" text
// messages over TCP.
package r16h

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
		Name: "r16h",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "$"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var patternGpsd = kit.NewPattern().
	Text("@GPSD,").
	Number("(d+),").           // imei
	Expression("([RS]),").     // real time / history
	Number("(dddd)(dd)(dd),"). // date
	Number("(dd)(dd)(dd),").   // time
	Number("(d+.d+),").        // latitude
	Expression("([NS]),").
	Number("(d+.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+),").       // speed
	Number("(d+),").       // course
	Number("(-?d+),").     // altitude
	Number("(d+),").       // battery
	Expression("([LR]),"). // strap locking status
	Expression(`(\w*)`).   // alarm flag
	Compile()

var patternLbsd = kit.NewPattern().
	Text("@LBSD,").
	Number("(d+),").           // imei
	Expression("([RS]),").     // real time / history
	Number("(dddd)(dd)(dd),"). // date
	Number("(dd)(dd)(dd),").   // time
	Number("(d+)-").           // mcc
	Number("(d+)-").           // mnc
	Number("(x+)-").           // lac
	Number("(x+):").           // cid
	Number("(-?d+),,,").       // rssi
	Number("(d+),").           // battery
	Expression("([LR]),").     // strap locking status
	Expression(`(\w*)`).       // alarm flag
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))
	switch {
	case strings.HasPrefix(sentence, "@LINK,"):
		m.Session(kit.SubstringFrom(sentence, len("@LINK,")))
		return nil
	case strings.HasPrefix(sentence, "@LBSD,"):
		return decodeLbsd(m, sentence)
	}
	return decodeGpsd(m, sentence)
}

func decodeGpsd(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternGpsd, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}
	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = true
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())
	decodeStatus(pos, parser)
	return pos
}

func decodeLbsd(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternLbsd, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}
	t := parser.NextDateTimeDefault()
	m.LastLocation(pos, &t)
	mcc, mnc := parser.MustInt(), parser.MustInt()
	lac, cid := parser.MustHexInt(), parser.MustHexInt()
	pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), parser.MustInt()))
	decodeStatus(pos, parser)
	return pos
}

func initPosition(m *ingest.Message, parser *kit.Parser) *model.Position {
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	if parser.Next() == "S" {
		pos.Set(model.KeyArchive, true)
	}
	return pos
}

func decodeStatus(pos *model.Position, parser *kit.Parser) {
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set("strapLocked", parser.Next() == "L")
	switch parser.Next() {
	case "SOS":
		pos.AddAlarm(model.AlarmSos)
	case "LBT":
		pos.AddAlarm(model.AlarmLowBattery)
	case "ULK":
		pos.AddAlarm(model.AlarmRemoving)
	}
}
