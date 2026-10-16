// Package tt8850 is the TT8850 protocol (Tt8850Protocol): "$"-terminated GT reports over
// TCP.
package tt8850

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tt8850",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "$"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Binary("0004,").
	Number("xxxx,").
	Expression("[01],").
	Expression("GT...,").
	Number("(?:[0-9A-Z]{2}xxxx)?,"). // protocol version
	Expression("([^,]+),").          // imei
	Any().
	Number("(d{1,2})?,").       // gps accuracy
	Number("(d{1,3}.d)?,").     // speed
	Number("(d{1,3})?,").       // course
	Number("(-?d{1,5}.d)?,").   // altitude
	Number("(-?d{1,3}.d{6}),"). // longitude
	Number("(-?d{1,2}.d{6}),"). // latitude
	Number("(dddd)(dd)(dd)").   // date (yyyymmdd)
	Number("(dd)(dd)(dd),").    // time (hhmmss)
	Number("(0ddd)?,").         // mcc
	Number("(0ddd)?,").         // mnc
	Number("(xxxx)?,").         // lac
	Number("(xxxx)?,").         // cell
	Any().
	Number("(dddd)(dd)(dd)"). // date (yyyymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	Number("(xxxx)").
	Compile()

// decode is Tt8850ProtocolDecoder.decode.
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

	pos.Valid = true
	pos.Accuracy = float64(parser.NextIntDefault(0))
	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.SetLatitude(parser.NextDoubleDefault(0))

	pos.SetTime(parser.NextDateTimeDefault())

	if parser.HasNextN(4) {
		mcc, mnc := parser.NextIntDefault(0), parser.NextIntDefault(0)
		lac, cid := parser.NextHexIntDefault(0), parser.NextHexIntDefault(0)
		pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	}

	return pos
}
