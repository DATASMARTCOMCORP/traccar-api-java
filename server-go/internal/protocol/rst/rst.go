// Package rst is the RST protocol (RstProtocol): "RST;...;FIM;" text datagrams.
package rst

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "rst",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("RST;").
	Expression("([AL]);").  // archive
	Expression("([^;]+);"). // model
	Expression("([^;]+);"). // firmware
	Number("(d{9});").      // serial number
	Number("(d+);").        // index
	Number("(d+);").        // type
	GroupBegin().
	Number("(dd)-(dd)-(dddd) "). // event date
	Number("(dd):(dd):(dd);").   // event time
	Number("(dd)-(dd)-(dddd) "). // fix date
	Number("(dd):(dd):(dd);").   // fix time
	Number("(-?d+.d+);").        // latitude
	Number("(-?d+.d+);").        // longitude
	Number("(d+);").             // speed
	Number("(d+);").             // course
	Number("(-?d+);").           // altitude
	Number("([01]);").           // valid
	Number("(d+);").             // satellites
	Number("(d+);").             // hdop
	Number("(xx);").             // inputs 1
	Number("(xx);").             // inputs 2
	Number("(xx);").             // inputs 3
	Number("(xx);").             // outputs 1
	Number("(xx);").             // outputs 2
	Number("(d+.d+);").          // power
	Number("(d+.d+);").          // battery
	Number("(d+);").             // odometer
	Number("(d+);").             // rssi
	Number("(xx);").             // temperature
	Number("x{4};").             // sensors
	Number("(xx);").             // status 1
	Number("(xx);").             // status 2
	Expression("(.*)").          // additional data
	GroupEndWith("?").
	Any().
	Text("FIM;").
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	parser.Next() // archive
	deviceModel := parser.Next()
	firmware := parser.Next()
	serial := parser.Next()
	index := parser.MustInt()
	typ := parser.MustInt()

	m.Send("RST;A;" + deviceModel + ";" + firmware + ";" + serial + ";" + strconv.Itoa(index) + ";6;FIM;")

	ds := m.Session(serial)
	if ds == nil {
		return nil
	}

	if parser.HasNext() {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.DeviceTime = model.NewTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
		pos.FixTime = model.NewTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
		pos.SetLatitude(parser.MustDouble())
		pos.SetLongitude(parser.MustDouble())
		pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
		pos.Course = float64(parser.MustInt())
		pos.Altitude = float64(parser.MustInt())
		pos.Valid = parser.MustInt() > 0

		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyHdop, parser.NextInt())

		inputs1 := parser.MustHexInt()
		inputs2 := parser.MustHexInt()
		inputs3 := parser.MustHexInt()
		pos.Set(model.PrefixIn+"1", inputs1)
		pos.Set(model.PrefixIn+"2", inputs2)
		pos.Set(model.PrefixIn+"3", inputs3)
		pos.Set(model.KeyIgnition, kit.Check(int64(inputs2), 7))

		pos.Set(model.PrefixOut+"1", parser.NextHexInt())
		pos.Set(model.PrefixOut+"2", parser.NextHexInt())
		pos.Set(model.KeyPower, parser.NextDouble())
		pos.Set(model.KeyBattery, parser.NextDouble())
		pos.Set(model.KeyOdometer, parser.NextInt())
		pos.Set(model.KeyRssi, parser.NextInt())
		pos.Set(model.PrefixTemp+"1", int(int8(parser.MustHexInt())))
		pos.Set(model.KeyStatus, (parser.MustHexInt()<<8)+parser.MustHexInt())

		values := kit.Split(parser.Next(), ";")
		if typ == 55 {
			pos.Set(model.KeyDriverUniqueID, values[0])
		}

		return pos
	} else if typ == 134 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, strconv.Itoa(typ))
		return pos
	}
	return nil
}
