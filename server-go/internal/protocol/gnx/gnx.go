// Package gnx is the GNX protocol (GnxProtocol): "$GNX_..." text sentences over TCP,
// delimited by "\n\r".
package gnx

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gnx",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\n\r"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var patternLocation = kit.NewPattern().
	Number("(d+),").         // imei
	Number("d+,").           // length
	Expression("([01]),").   // history
	Number("(dd)(dd)(dd),"). // device time (hhmmss)
	Number("(dd)(dd)(dd),"). // device date (ddmmyy)
	Number("(dd)(dd)(dd),"). // fix time (hhmmss)
	Number("(dd)(dd)(dd),"). // fix date (ddmmyy)
	Number("(d),").          // valid
	Number("(dd.d+),").      // latitude
	Expression("([NS]),").   //
	Number("(ddd.d+),").     // longitude
	Expression("([EW]),").   //
	String()

var patternMIF = kit.NewPattern().
	Text("$GNX_MIF,").
	Expression(patternLocation).
	Expression("[01],").    // valid card
	Expression("([^,]+),"). // rfid
	Any().
	Compile()

var patternOther = kit.NewPattern().
	Text("$GNX_").
	Expression("...,").
	Expression(patternLocation).
	Any().
	Compile()

var zone = javatime.TimeZone("GMT+5:30")

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	typ := kit.Substring(sentence, 5, 8)

	pattern := patternOther
	if typ == "MIF" {
		pattern = patternMIF
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	if parser.NextIntDefault(0) == 1 {
		pos.Set(model.KeyArchive, true)
	}

	pos.DeviceTime = model.NewTime(parser.NextDateTime(kit.HMS_DMY, zone, m.Now))
	pos.FixTime = model.NewTime(parser.NextDateTime(kit.HMS_DMY, zone, m.Now))

	pos.Valid = parser.NextIntDefault(0) != 0

	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))

	if typ == "MIF" {
		pos.Set(model.KeyDriverUniqueID, parser.Next())
	}

	return pos
}
