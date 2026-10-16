// Package gotop is the Gotop protocol (GotopProtocol): text reports over TCP, delimited
// by '#'.
package gotop

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
		Name: "gotop",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "#"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("(d+),").                   // imei
	Expression("([^,]+),").            // type
	Expression("([AV]),").             // validity
	Text("DATE:").Optional().          //
	Number("(dd)(dd)(dd),").           // date (yyddmm)
	Text("TIME:").Optional().          //
	Number("(dd)(dd)(dd),").           // time (hhmmss)
	Text("LAT:").Optional().           //
	Number("(d+.d+)([NS]),").          // latitude
	Expression("LO[NT]:").Optional().  //
	Number("(d+.d+)([EW]),").          // longitude
	Text("Speed:").Number("(d+.d+),"). // speed
	GroupBegin().
	Number("(d+)-"). // battery
	Number("(d+),"). // rssi
	GroupBegin().
	Number("(d+.d),"). // altitude
	Number("(d+.dd)"). // hdop
	GroupEndWith("?").
	GroupEndWith("?").
	Any().
	Compile()

// numericValue is Character.getNumericValue for the BMP: digits, Latin letters (10 to
// 35) and -1 for anything else (other Unicode numerics are not expected here).
func numericValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	case c >= 0xFF21 && c <= 0xFF3A: // fullwidth A-Z
		return int(c-0xFF21) + 10
	case c >= 0xFF41 && c <= 0xFF5A: // fullwidth a-z
		return int(c-0xFF41) + 10
	}
	return -1
}

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

	typ := parser.Next()
	if typ == "CMD-KEY" {
		pos.AddAlarm(model.AlarmSos)
	} else if strings.HasPrefix(typ, "ALM-B") {
		if numericValue(kit.CharAt(typ, 5))%2 > 0 {
			pos.AddAlarm(model.AlarmGeofenceEnter)
		} else {
			pos.AddAlarm(model.AlarmGeofenceExit)
		}
	}

	pos.Valid = parser.Next() == "A"

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = units.KnotsFromKph(parser.MustDouble())

	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Set(model.KeyHdop, parser.NextDouble())

	return pos
}
