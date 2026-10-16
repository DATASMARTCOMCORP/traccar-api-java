// Package stl060 is the STL060 protocol (Stl060Protocol): '#'-terminated text records over TCP.
package stl060

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "stl060",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is Stl060FrameDecoder: '#'-delimited frames, starting at the first '$'.
func Framer(maxLength int) ingest.Framer {
	delimiter := ingest.NewDelimiterFramer(maxLength, "#")
	return ingest.FramerFunc(func(m *ingest.Message, buf *kit.Buf) any {
		r := delimiter.Frame(m, buf)
		if r == nil {
			return nil
		}
		result := r.(*kit.Buf)
		index := result.IndexOf(result.ReaderIndex(), result.WriterIndex(), '$')
		if index == -1 {
			return result
		}
		result.Skip(index)
		return result.ReadSlice(result.ReadableBytes())
	})
}

var pattern = kit.NewPattern().
	Any().
	Text("$1,").
	Number("(d+),").                  // imei
	Text("D001,").                    // type
	Expression("[^,]*,").             // vehicle
	Number("(dd)/(dd)/(dd),").        // date (dd/mm/yy)
	Number("(dd):(dd):(dd),").        // time (hh:mm:ss)
	Number("(dd)(dd).?(d+)([NS]),").  // latitude
	Number("(ddd)(dd).?(d+)([EW]),"). // longitude
	Number("(d+.?d*),").              // speed
	Number("(d+.?d*),").              // course
	GroupBegin().
	Number("(d+),"). // odometer
	Number("(d+),"). // ignition
	Number("(d+),"). // di1
	Number("(d+),"). // di2
	Number("(d+),"). // fuel
	Or().
	Expression("([01]),").  // charging
	Expression("([01]),").  // ignition
	Expression("0,0,").     // reserved
	Number("(d+),").        // di
	Expression("([^,]+),"). // rfid
	Number("(d+),").        // odometer
	Number("(d+),").        // temperature
	Number("(d+),").        // fuel
	Expression("([01]),").  // accelerometer
	Expression("([01]),").  // do1
	Expression("([01]),").  // do2
	GroupEnd().
	Expression("([AV])"). // validity
	Any().
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

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	// Old format
	if parser.HasNextN(5) {
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
		pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)
		pos.Set(model.KeyInput, (parser.NextIntDefault(0)+parser.NextIntDefault(0))<<1)
		pos.Set(model.KeyFuel, parser.NextIntDefault(0))
	}

	// New format
	if parser.HasNextN(10) {
		pos.Set(model.KeyCharge, parser.NextIntDefault(0) == 1)
		pos.Set(model.KeyIgnition, parser.NextIntDefault(0) == 1)
		pos.Set(model.KeyInput, parser.NextIntDefault(0))
		pos.Set(model.KeyDriverUniqueID, parser.Next())
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
		pos.Set(model.PrefixTemp+"1", parser.NextIntDefault(0))
		pos.Set(model.KeyFuel, parser.NextIntDefault(0))
		pos.Set(model.KeyAcceleration, parser.NextIntDefault(0) == 1)
		pos.Set(model.KeyOutput, (parser.NextIntDefault(0)+parser.NextIntDefault(0))<<1)
	}

	pos.Valid = parser.Next() == "A"
	return pos
}
