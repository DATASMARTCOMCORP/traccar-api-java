// Package idpl is the IDPL protocol (IdplProtocol): "*ID...#" text lines.
package idpl

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server of lines.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "idpl",
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
	Text("*ID").                            // start of frame
	Number("(d+),").                        // command code
	Number("(d+),").                        // imei
	Number("(dd)(dd)(dd),").                // current date (ddmmyy)
	Number("(dd)(dd)(dd),").                // current time (hhmmss)
	Expression("([A|V]),").                 // gps fix
	Number("(dd)(dd).?(d+),([NS]),").       // latitude
	Number("(ddd)(dd).?(d+),([EW]),").      // longitude
	Number("(d{1,3}.dd),").                 // speed
	Number("(d{1,3}.dd),").                 // course
	Number("(d{1,2}),").                    // sats
	Number("(d{1,3}),").                    // gsm signal strength
	Expression("([A|N|S]),").               // vehicle status
	Expression("([0|1]),").                 // main power status
	Number("(d.dd),").                      // internal battery voltage
	Expression("([0|1]),").                 // sos alert
	Expression("([0|1]),").                 // body tamper
	Expression("([0|1])([0|1]),").          // ac status + ign status
	Expression("([0|1|2]),").               // output1 status
	Number("(d{1,3}),").                    // adc1
	Number("(d{1,3}),").                    // adc2
	Expression("([0-9A-Z]{3}),").           // software version
	Expression("([L|R]),").                 // message type
	Number("(x{4})#").                      // crc
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.Set(model.KeyType, parser.NextIntDefault(0))

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))
	pos.Set("vehicleStatus", parser.Next())
	pos.Set(model.KeyPower, parser.NextIntDefault(0))
	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0))
	if parser.NextIntDefault(0) == 1 {
		pos.AddAlarm(model.AlarmSos)
	}
	parser.NextIntDefault(0) // body tamper
	pos.Set("acStatus", parser.NextIntDefault(0))
	pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)
	pos.Set(model.KeyOutput, parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"1", parser.NextIntDefault(0))
	pos.Set(model.PrefixAdc+"2", parser.NextIntDefault(0))
	pos.Set(model.KeyVersionFw, parser.Next())
	pos.Set(model.KeyArchive, parser.Next() == "R")

	parser.Next() // checksum

	return pos
}
