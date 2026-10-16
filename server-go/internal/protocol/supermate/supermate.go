// Package supermate is the Supermate protocol (SupermateProtocol): '#'-delimited text with
// hexadecimal fields over TCP.
package supermate

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "supermate",
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
	Number("d+:").   // header
	Number("(d+):"). // imei
	Number("d+:").Text("*,").
	Number("(d+),").          // command id
	Expression("([^,]{2}),"). // command
	Expression("([AV]),").    // validity
	Number("(xx)(xx)(xx),").  // date (yymmdd)
	Number("(xx)(xx)(xx),").  // time (hhmmss)
	Number("(x)(x{7}),").     // latitude
	Number("(x)(x{7}),").     // longitude
	Number("(x{4}),").        // speed
	Number("(x{4}),").        // course
	Number("(x{12}),").       // status
	Number("(x+),").          // signal
	Number("(d+),").          // power
	Number("(x{4}),").        // oil
	Number("(x+)?").          // odometer
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	imei := parser.Next()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set("commandId", parser.Next())
	pos.Set(model.KeyCommand, parser.Next())
	pos.Valid = parser.Next() == "A"

	date := kit.NewDateBuilder(nil).
		SetDate(parser.NextHexIntDefault(0), parser.NextHexIntDefault(0), parser.NextHexIntDefault(0)).
		SetTime(parser.NextHexIntDefault(0), parser.NextHexIntDefault(0), parser.NextHexIntDefault(0))
	pos.SetTime(date.Date())

	latitudeNegative := parser.NextHexIntDefault(0) == 8
	latitude := float64(parser.NextHexIntDefault(0)) / 600000.0
	if latitudeNegative {
		latitude = -latitude
	}
	pos.SetLatitude(latitude)
	longitudeNegative := parser.NextHexIntDefault(0) == 8
	longitude := float64(parser.NextHexIntDefault(0)) / 600000.0
	if longitudeNegative {
		longitude = -longitude
	}
	pos.SetLongitude(longitude)

	pos.Speed = float64(parser.NextHexIntDefault(0)) / 100.0
	pos.Course = float64(parser.NextHexIntDefault(0)) / 100.0

	pos.Set(model.KeyStatus, parser.Next())
	pos.Set("signal", parser.Next())
	pos.Set(model.KeyPower, parser.NextDoubleDefault(0))
	pos.Set("oil", parser.NextHexIntDefault(0))
	pos.Set(model.KeyOdometer, parser.NextHexIntDefault(0))

	now := m.Now.In(m.Zone())
	content := kit.Format("#1:%s:1:*,00000000,UP,%02x%02x%02x,%02x%02x%02x#", imei,
		now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())
	m.Send(kit.NewBuf(kit.NettyBytes(content)))

	return pos
}
