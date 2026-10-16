// Package net is the Net protocol (NetProtocol): "@L" text messages ending with '!'.
package net

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "net",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "!"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("@L").
	Number("ddd").
	Number("(d{15})"). // imei
	Number("xx").
	Number("(dd)(dd)(dd)").    // date (ddmmyy)
	Number("(dd)(dd)(dd)").    // time (hhmmss)
	Number("(x)").             // flags
	Number("(dd)(dd)(dddd)").  // latitude
	Number("(ddd)(dd)(dddd)"). // longitude
	Number("(x{8})").          // status
	Number("(x{4})").          // speed
	Number("(x{6})").          // odometer
	Number("(xxx)").           // course
	Number("(xxx)").           // alarm
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	flags := int64(parser.MustHexInt())

	pos.Valid = kit.Check(flags, 3)
	hemisphereLatitude := 1.0
	if kit.Check(flags, 1) {
		hemisphereLatitude = -1
	}
	hemisphereLongitude := 1.0
	if kit.Check(flags, 0) {
		hemisphereLongitude = -1
	}

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinMin) * hemisphereLatitude)
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinMin) * hemisphereLongitude)

	pos.Set(model.KeyStatus, parser.NextHexLong())
	pos.Speed = float64(parser.MustHexInt()) / 100.0
	pos.Set(model.KeyOdometer, float64(parser.MustHexInt())*1852.0/16)
	pos.Course = float64(parser.MustHexInt())

	parser.NextHexInt() // alarm

	return pos
}
