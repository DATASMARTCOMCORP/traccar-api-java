// Package armoli is the Armoli protocol (ArmoliProtocol): text reports over TCP, polled by
// the server.
package armoli

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "armoli",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ";;", ";\r", ";"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				// ArmoliProtocolPoller: interval 180000, in seconds as BaseProtocolPoller
				// reads it (about 50 hours).
				Poll: &ingest.Poller{Interval: 180000 * time.Second, Request: func(m *ingest.Message) {
					m.Send("[TX,];;")
				}},
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("[M").                              // start
	Number("(d{15})").                       // imei
	Number("(dd)(dd)(dd)").                  // date (ddmmyy)
	Number("(dd)(dd)(dd)").                  // time (hhmmss)
	Number("([NS])(dd.d{6})").               // latitude
	Number("([EW])(ddd.d{6})").              // longitude
	Number("(d)").                           // valid
	Number("(x)").                           // satellites
	Number("(xx)").                          // speed
	Number("(ddd)").                         // course
	Number("(xxx)").                         // adc 1
	Number("(xxx)").                         // adc 2
	Number("(xx)").                          // status
	Number("(xx)").                          // max speed
	Number("(x{6})").                        // distance
	Number("(dd)?").                         // hdop
	Number("x{4}").                          // idle
	Number(":(x+)").Optional().              // alarms
	Number("G(x{6})").Optional().            // g-sensor
	Number("H(x{3})").Optional().            // power
	Number("E(x{3})").Optional().            // battery
	Number("!(x+)").Optional().              // driver
	Expression("@A([>0-9A-F]+)").Optional(). // obd
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	typ := kit.CharAt(sentence, 1)
	pos := m.NewPosition()

	if typ != 'M' {
		if typ == 'W' {
			if ds := m.Session(); ds != nil {
				pos.DeviceID = ds.DeviceID
				m.LastLocation(pos, nil)
				pos.Set(model.KeyResult, kit.Substring(sentence, kit.IndexOf(sentence, ",")+1, kit.Len(sentence)-1))
				return pos
			}
		} else if typ == 'Q' || typ == 'L' {
			m.Send("[TX,];;")
		}
		return nil
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))
	pos.Valid = parser.MustInt() > 0
	pos.Set(model.KeySatellites, parser.NextHexInt())
	pos.Speed = units.KnotsFromKph(float64(parser.MustHexInt()))
	pos.Course = float64(parser.MustInt())
	pos.Set(model.PrefixAdc+"1", float64(parser.MustHexInt())/27.0*1000)
	pos.Set(model.PrefixAdc+"1", float64(parser.MustHexInt())/27.0*1000)
	pos.Set(model.KeyStatus, parser.NextHexInt())
	pos.Set("maxSpeed", parser.NextHexInt())
	pos.Set(model.KeyOdometer, parser.NextHexInt())
	if parser.HasNext() {
		pos.Set(model.KeyHdop, float64(parser.MustInt())/10.0)
	}
	if parser.HasNext() {
		pos.Set("alarms", parser.Next())
	}
	if parser.HasNext() {
		pos.Set(model.KeyGSensor, parser.Next())
	}
	if parser.HasNext() {
		pos.Set(model.KeyPower, float64(parser.MustHexInt())/100.0)
	}
	if parser.HasNext() {
		pos.Set(model.KeyBattery, float64(parser.MustHexInt())/100.0)
	}
	if parser.HasNext() {
		pos.Set(model.KeyDriverUniqueID, parser.Next())
	}
	if parser.HasNext() {
		values := kit.Split(parser.Next(), ">")
		for _, value := range values[1:] {
			kit.ObdDecodeData(kit.IntRadix(kit.Substring(value, 4, 6), 16), kit.LongRadix(kit.SubstringFrom(value, 6), 16), true).Add(pos)
		}
	}
	return pos
}
