// Package haicom is the Haicom protocol (HaicomProtocol): "$GPRS" text messages ending
// with '*'.
package haicom

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "haicom",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "*"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("$GPRS").
	Number("(d+),").            // imei
	Expression("([^,]+),").     // version
	Number("(dd)(dd)(dd),").    // date (yymmdd)
	Number("(dd)(dd)(dd),").    // time (hhmmss)
	Number("(d)").              // flags
	Number("(dd)(d{5})").       // latitude
	Number("(ddd)(d{5}),").     // longitude
	Number("(d+),").            // speed
	Number("(d+),").            // course
	Number("(d+),").            // status
	Number("(d+)?,").           // gprs counting value
	Number("(d+)?,").           // gps power saving counting value
	Number("(d+),").            // switch status
	Number("(d+)").             // relay status
	Expression("(?:[LH]{2})?"). // power status
	Number("#V(d+)").           // battery
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

	pos.Set(model.KeyVersionFw, parser.Next())
	pos.SetTime(parser.NextDateTimeDefault())

	flags := int64(parser.NextIntDefault(0))
	pos.Valid = kit.Check(flags, 0)

	latitude := parser.NextDoubleDefault(0) + parser.NextDoubleDefault(0)/60000
	if kit.Check(flags, 2) {
		pos.SetLatitude(latitude)
	} else {
		pos.SetLatitude(-latitude)
	}
	longitude := parser.NextDoubleDefault(0) + parser.NextDoubleDefault(0)/60000
	if kit.Check(flags, 1) {
		pos.SetLongitude(longitude)
	} else {
		pos.SetLongitude(-longitude)
	}

	pos.Speed = parser.NextDoubleDefault(0) / 10
	pos.Course = parser.NextDoubleDefault(0) / 10

	pos.Set(model.KeyStatus, parser.Next())
	pos.Set("gprsCount", parser.NextNullable())
	pos.Set("powersaveCountdown", parser.NextNullable())
	pos.Set(model.KeyInput, parser.Next())
	pos.Set(model.KeyOutput, parser.Next())
	pos.Set(model.KeyBattery, parser.NextDoubleDefault(0)/10.0)
	return pos
}
