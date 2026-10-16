// Package avema is the Avema protocol (AvemaProtocol): comma-separated text lines over
// TCP.
package avema

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "avema",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Number("(d+),").               // device id
	Number("(dddd)(dd)(dd)").      // date (yyyymmdd)
	Number("(dd)(dd)(dd),").       // time (hhmmss)
	Number("(-?d+.d+),").          // longitude
	Number("(-?d+.d+),").          // latitude
	Number("(d+),").               // speed
	Number("(d+),").               // course
	Number("(-?d+),").             // altitude
	Number("(d+),").               // satellites
	Number("(d+),").               // event
	Number("(d+.d+),").            // odometer
	Number("(d+),").               // input
	Number("(d+.d+)V?,").          // adc 1
	Number("(d+.d+)V?,").          // adc 2
	Number("(d+),").               // output
	Number("(d),").                // roaming
	Number("(d+),").               // rssi
	Number("d,").                  // communication system
	Number("(ddd)-?").             // mcc
	Number("(d+),").               // mnc
	Number("(x+),").               // lac
	Number("(x+),").               // cid
	Number("(d+.d+),").Optional(). // battery
	Number("([^,]+)?").            // rfid
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

	pos.Valid = true
	pos.SetTime(parser.NextDateTimeDefault())
	pos.SetLongitude(parser.MustDouble())
	pos.SetLatitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyEvent, parser.NextInt())
	pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	pos.Set(model.KeyInput, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.NextDouble())
	pos.Set(model.PrefixAdc+"2", parser.NextDouble())
	pos.Set(model.KeyOutput, parser.NextInt())
	pos.Set(model.KeyRoaming, parser.MustInt() == 1)

	rssi := parser.MustInt()
	mcc, mnc := parser.MustInt(), parser.MustInt()
	lac, cid := parser.MustHexInt(), parser.MustHexInt()
	pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))

	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyDriverUniqueID, parser.Next())
	return pos
}
