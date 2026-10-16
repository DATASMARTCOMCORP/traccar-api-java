// Package nto is the NTO protocol (NtoProtocol): "NB," text reports over TCP.
package nto

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "nto",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "&"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("^NB,").                   // manufacturer
	Number("(d+),").                // imei
	Expression("(...),").           // type
	Number("(dd)(dd)(dd),").        // date (ddmmyy)
	Number("(dd)(dd)(dd),").        // time (hhmmss)
	Expression("([AVM]),").         // validity
	Number("([NS]),(dd)(dd.d+),").  // latitude
	Number("([EW]),(ddd)(dd.d+),"). // longitude
	Number("(d+.?d*),").            // speed
	Number("(d+),").                // course
	Number("(x+),").                // status
	Any().
	Compile()

func alarmIf(pos *model.Position, on bool, alarm string) {
	if on {
		pos.AddAlarm(alarm)
	}
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
	pos.Set(model.KeyType, parser.Next())
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Speed = parser.MustDouble()
	pos.Course = float64(parser.MustInt())

	status := parser.MustHexLong()
	pos.Set(model.KeyStatus, status)
	alarmIf(pos, kit.Check(status, 1), model.AlarmJamming)
	alarmIf(pos, kit.Check(status, 3*8+1), model.AlarmPowerCut)
	alarmIf(pos, kit.Check(status, 3*8+2), model.AlarmOverspeed)
	alarmIf(pos, kit.Check(status, 3*8+3), model.AlarmVibration)
	alarmIf(pos, kit.Check(status, 3*8+4), model.AlarmGeofenceEnter)
	alarmIf(pos, kit.Check(status, 3*8+5), model.AlarmGeofenceExit)
	alarmIf(pos, kit.Check(status, 4*8), model.AlarmLowBattery)
	alarmIf(pos, kit.Check(status, 4*8+4), model.AlarmDoor)
	return pos
}
