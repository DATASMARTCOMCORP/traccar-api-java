// Package visiontek is the Visiontek protocol (VisiontekProtocol): "$1,..." text
// messages delimited by '#' over TCP.
package visiontek

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
		Name: "visiontek",
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
	Text("$1,").
	Expression("([^,]+),").     // identifier
	Number("(d+),").Optional(). // imei
	Number("(dd),(dd),(dd),").  // date (dd,mm,yy)
	Number("(dd),(dd),(dd),").  // time (hh,mm,ss)
	GroupBegin().
	Number("(dd)(dd).?(d+)([NS]),").  // latitude
	Number("(ddd)(dd).?(d+)([EW]),"). // longitude
	Or().
	Number("(dd.d+)([NS]),").  // latitude
	Number("(ddd.d+)([EW]),"). // longitude
	GroupEnd().
	Number("(d+.?d+),"). // speed
	Number("(d+),").     // course
	GroupBegin().
	Number("(d+),").   // altitude
	Number("(d+),").   // satellites
	Number("(d+),").   // odometer
	Number("([01]),"). // ignition
	Number("([01]),"). // input 1
	Number("([01]),"). // input 2
	Number("([01]),"). // immobilizer
	Number("([01]),"). // external battery status
	Number("(d+),").   // gsm
	Or().
	Number("(d+.d),").                // hdop
	Number("(d+),").                  // altitude
	Number("(d+),").                  // odometer
	Number("([01],[01],[01],[01]),"). // input
	Number("([01],[01],[01],[01]),"). // output
	Number("(d+.?d*),").              // adc 1
	Number("(d+.?d*),").              // adc 2
	GroupEndWith("?").
	Any().
	Expression("([AV])").          // validity
	Number(",(d{10})").Optional(). // rfid
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(parser.Next(), parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	if parser.HasNextN(8) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinMinHem))
	}
	if parser.HasNextN(4) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	}

	pos.Speed = units.KnotsFromKph(kit.Double(strings.ReplaceAll(parser.Next(), ".", "")) / 10)

	pos.Course = parser.NextDoubleDefault(0)

	if parser.HasNextN(9) {
		pos.Altitude = parser.NextDoubleDefault(0)
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0)*1000)
		pos.Set(model.KeyIgnition, parser.Next() == "1")
		pos.Set(model.PrefixIo+"1", parser.NextNullable())
		pos.Set(model.PrefixIo+"2", parser.NextNullable())
		pos.Set("immobilizer", parser.NextNullable())
		pos.Set(model.KeyCharge, parser.Next() == "1")
		pos.Set(model.KeyRssi, parser.NextDouble())
	}

	if parser.HasNextN(7) {
		pos.Set(model.KeyHdop, parser.NextDouble())
		pos.Altitude = parser.NextDoubleDefault(0)
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0)*1000)
		pos.Set(model.KeyInput, parser.NextNullable())
		pos.Set(model.KeyOutput, parser.NextNullable())
		pos.Set(model.PrefixAdc+"1", parser.NextNullable())
		pos.Set(model.PrefixAdc+"2", parser.NextNullable())
	}

	pos.Valid = parser.Next() == "A"

	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())

	return pos
}
