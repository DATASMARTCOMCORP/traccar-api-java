// Package flexcomm is the FlexComm protocol (FlexCommProtocol): fixed-length text
// reports of 110 chars.
package flexcomm

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "flexcomm",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.FixedFramer{Length: 2 + 2 + 101 + 5},
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("7E").
	Number("(dd)").                       // status
	Number("(d{15})").                    // imei
	Number("(dddd)(dd)(dd)").             // date (yyyymmdd)
	Number("(dd)(dd)(dd)").               // time (hhmmss)
	Expression("([01])").                 // valid
	Number("(d{9})").                     // latitude
	Number("(d{10})").                    // longitude
	Number("(d{4})").                     // altitude
	Number("(ddd)").                      // speed
	Number("(ddd)").                      // course
	Number("(dd)").                       // satellites view
	Number("(dd)").                       // satellites used
	Number("(dd)").                       // rssi
	Number("(ddd)").                      // mcc
	Number("(ddd)").                      // mnc
	Number("(x{6})").                     // lac
	Number("(x{6})").                     // cid
	Expression("([01])([01])([01])").     // input
	Expression("([01])([01])").           // output
	Number("(ddd)").                      // fuel
	Number("(d{4})").                     // temperature
	Number("(ddd)").                      // battery
	Number("(ddd)").                      // power
	Any().
	Compile()

// scales are Math.pow(10, -decimalPoints) for the decimal points used.
var scales = map[int]float64{0: 1, 6: 1e-6}

func parseSignedValue(parser *kit.Parser, decimalPoints int) float64 {
	s := parser.Next()
	negative := kit.CharAt(s, 0) == '1'
	value := float64(kit.Int(kit.SubstringFrom(s, 1))) * scales[decimalPoints]
	if negative {
		return -value
	}
	return value
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.Set(model.KeyStatus, parser.NextInt())

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "1"
	pos.SetLatitude(parseSignedValue(parser, 6))
	pos.SetLongitude(parseSignedValue(parser, 6))
	pos.Altitude = parseSignedValue(parser, 0)
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellitesVisible, parser.NextInt())
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())

	mcc, mnc := parser.MustInt(), parser.MustInt()
	lac, cid := parser.MustHexInt(), parser.MustHexInt()
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))

	for i := 1; i <= 3; i++ {
		pos.Set(model.PrefixIn+strconv.Itoa(i), parser.NextInt())
	}
	for i := 1; i <= 2; i++ {
		pos.Set(model.PrefixOut+strconv.Itoa(i), parser.NextInt())
	}

	pos.Set(model.KeyFuel, parser.NextInt())
	pos.Set(model.PrefixTemp+"1", parseSignedValue(parser, 0))
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.KeyPower, float64(parser.MustInt())/10.0)

	m.Send("{01}")

	return pos
}
