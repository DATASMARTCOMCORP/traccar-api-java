// Package disha is the Disha protocol (DishaProtocol): '#'-separated text lines over TCP.
package disha

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "disha",
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
	Text("$A#A#").
	Number("(d+)#").         // imei
	Expression("([AVMX])#"). // validity
	Number("(dd)(dd)(dd)#"). // time (hhmmss)
	Number("(dd)(dd)(dd)#"). // date (ddmmyy)
	Number("(dd)(dd.d+)#").  // latitude
	Expression("([NS])#").   //
	Number("(ddd)(dd.d+)#"). // longitude
	Expression("([EW])#").   //
	Number("(d+.d+)#").      // speed
	Number("(d+.d+)#").      // course
	Number("(d+)#").         // satellites
	Number("(d+.d+)#").      // hdop
	Number("(d+)#").         // gsm
	Expression("([012])#").  // power mode
	Number("(d+)#").         // battery
	Number("(d+)#").         // adc 1
	Number("(d+)#").         // adc 2
	Number("d+.d+#").        // day distance
	Number("(d+.d+)#").      // odometer
	Expression("([01]+)").   // digital inputs
	Text("*").
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Valid = parser.Next() == "A"
	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Set(model.KeyRssi, parser.NextDouble())
	pos.Set(model.KeyCharge, parser.NextIntDefault(0) == 2)
	pos.Set(model.KeyBatteryLevel, parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"1", parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"2", parser.NextIntDefault(0))
	pos.Set(model.KeyOdometer, parser.NextDoubleDefault(0)*1000)
	pos.Set(model.KeyInput, parser.Next())
	return pos
}
