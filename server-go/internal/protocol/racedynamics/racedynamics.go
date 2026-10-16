// Package racedynamics is the Race Dynamics protocol (RaceDynamicsProtocol): "$GPRMC"
// style text lines over TCP.
package racedynamics

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "racedynamics",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLengthLarge),
				Text:    true,
				TextOut: true,
				Decoder: &Decoder{},
			}
		}}},
	}
}

const (
	msgLogin    = 12
	msgLocation = 15
)

var patternLogin = kit.NewPattern().
	Text("$GPRMC,").
	Number("d+,").   // type
	Number("d{6},"). // date
	Number("d{6},"). // time
	Number("(d{15}),").
	Compile()

var patternLocation = kit.NewPattern().
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([AV]),").   // validity
	Number("(dd)(dd.d+),").  // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+),").         // speed
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(-?d+),").       // altitude
	Number("(d+),").         // satellites
	Number("([01]),").       // ignition
	Number("(d+),").         // index
	Text("%,").
	Number("([^,]+),"). // ibutton
	Number("d+,").      // acceleration
	Number("d+,").      // deceleration
	Number("[01],").    // cruise control
	Number("[01],").    // seat belt
	Number("[01],").    // wrong ibutton
	Number("(d+),").    // power
	Number("[01],").    // power status
	Number("(d+),").    // battery
	Number("([01]),").  // panic
	Number("d+,").
	Number("d+,").
	Number("(d),").      // overspeed
	Number("d+,").       // speed limit
	Number("d+,").       // tachometer
	Number("d+,d+,d+,"). // aux
	Number("d+,").       // geofence id
	Number("d+,").       // road speed type
	Number("d+,").       // ibutton count
	Number("(d),").      // overdriver alert
	Any().
	Compile()

// Decoder is RaceDynamicsProtocolDecoder: it remembers the login's IMEI for replies.
type Decoder struct {
	imei *string
}

func (d *Decoder) sendResponse(m *ingest.Message, typ int) {
	if !m.HasChannel() {
		return
	}
	imei := "null"
	if d.imei != nil {
		imei = *d.imei
	}
	now := m.Now.In(m.Zone())
	m.Send(kit.Format("$GPRMC,%d,%s,%s,%s,\r\n", typ, now.Format("020106"), now.Format("150405"), imei))
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	typ := kit.Int(kit.Substring(sentence, 7, 9))
	switch typ {
	case msgLogin:
		parser := kit.NewParser(patternLogin, sentence)
		if parser.Matches() {
			imei := parser.Next()
			d.imei = &imei
			m.Session(imei)
			d.sendResponse(m, typ)
		}
	case msgLocation:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		positions := []*model.Position{}
		for _, data := range kit.Split(kit.Substring(sentence, 17, kit.Len(sentence)-3), ",#,#,") {
			parser := kit.NewParser(patternLocation, data)
			if !parser.Matches() {
				continue
			}
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			b := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
			pos.Valid = parser.Next() == "A"
			pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
			pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
			pos.Speed = parser.MustDouble()
			b.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
			pos.SetTime(b.Date())
			pos.Altitude = float64(parser.MustInt())
			pos.Set(model.KeySatellites, parser.NextInt())
			pos.Set(model.KeyIgnition, parser.MustInt() == 1)
			pos.Set(model.KeyIndex, parser.NextInt())
			pos.Set(model.KeyDriverUniqueID, parser.Next())
			pos.Set(model.KeyPower, float64(parser.MustInt())/100.0)
			pos.Set(model.KeyBattery, float64(parser.MustInt())/100.0)
			if parser.MustInt() > 0 {
				pos.AddAlarm(model.AlarmSos)
			}
			if parser.MustInt() > 0 {
				pos.AddAlarm(model.AlarmOverspeed)
			}
			if overDriver := parser.MustInt(); overDriver > 0 {
				pos.Set("overDriver", overDriver)
			}
			positions = append(positions, pos)
		}
		d.sendResponse(m, typ)
		return positions
	}
	return nil
}
