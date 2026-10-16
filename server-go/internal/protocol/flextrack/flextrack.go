// Package flextrack is the Flextrack protocol (FlextrackProtocol): text messages ending
// with '\r', a LOGON that identifies the device and UNITSTAT reports.
package flextrack

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
		Name: "flextrack",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\r"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var patternLogon = kit.NewPattern().
	Number("(-?d+),"). // index
	Text("LOGON,").
	Number("(d+),"). // node id
	Number("(d+)").  // iccid
	Compile()

var pattern = kit.NewPattern().
	Number("(-?d+),"). // index
	Text("UNITSTAT,").
	Number("(dddd)(dd)(dd),").     // date (yyyymmdd)
	Number("(dd)(dd)(dd),").       // time (hhmmss)
	Number("d+,").                 // node id
	Number("([NS])(d+).(d+.d+),"). // latitude
	Number("([EW])(d+).(d+.d+),"). // longitude
	Number("(d+),").               // speed
	Number("(d+),").               // course
	Number("(d+),").               // satellites
	Number("(d+),").               // battery
	Number("(-?d+),").             // gsm
	Number("(x+),").               // state
	Number("(ddd)").               // mcc
	Number("(dd),").               // mnc
	Number("(-?d+),").             // altitude
	Number("(d+),").               // hdop
	Number("(x+),").               // cell
	Number("d+,").                 // gps fix time
	Number("(x+),").               // lac
	Number("(d+)").                // odometer
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	switch {
	case strings.Contains(sentence, "LOGON"):
		parser := kit.NewParser(patternLogon, sentence)
		if !parser.Matches() {
			return nil
		}
		m.Send(parser.Next() + ",ACK\r")
		id := parser.Next()
		iccid := parser.Next()
		m.Session(iccid, id)

	case strings.Contains(sentence, "UNITSTAT"):
		ds := m.Session()
		if ds == nil {
			return nil
		}
		parser := kit.NewParser(pattern, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.Send(parser.Next() + ",ACK\r")

		pos.SetTime(parser.NextDateTimeDefault())

		pos.Valid = true
		pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
		pos.Speed = units.KnotsFromKph(float64(parser.NextIntDefault(0)))
		pos.Course = float64(parser.NextIntDefault(0))

		pos.Set(model.KeySatellites, parser.NextIntDefault(0))
		pos.Set(model.KeyBattery, parser.NextIntDefault(0))
		rssi := parser.NextIntDefault(0)
		pos.Set(model.KeyStatus, parser.NextHexIntDefault(0))

		mcc := parser.NextIntDefault(0)
		mnc := parser.NextIntDefault(0)

		pos.Altitude = float64(parser.NextIntDefault(0))

		pos.Set(model.KeyHdop, float64(parser.NextIntDefault(0))/10.0)

		lac := parser.NextHexIntDefault(0)
		cid := parser.NextHexIntDefault(0)
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))

		pos.Set(model.KeyOdometer, parser.NextIntDefault(0))

		return pos
	}

	return nil
}
