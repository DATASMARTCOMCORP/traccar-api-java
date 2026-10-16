// Package opengts is the OpenGTS protocol (OpenGtsProtocol): HTTP requests whose query
// carries the device id and a $GPRMC sentence.
package opengts

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "opengts", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

var pattern = kit.NewPattern().
	Text("$GPRMC,").
	Number("(dd)(dd)(dd)(?:.d+)?,"). // time (hhmmss)
	Expression("([AV]),").           // validity
	Number("(d+)(dd.d+),").          // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+),").      // speed
	Number("(d+.d+)?,").     // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Any().
	Compile()

var (
	ok         = &ingest.HTTPResponse{Status: http.StatusOK}
	badRequest = &ingest.HTTPResponse{Status: http.StatusBadRequest}
)

// Decoder is OpenGtsProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes one request; Java's exceptions are panics.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, _ []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	params, err := kit.QueryParams(kit.Latin1([]byte(r.RequestURI)), true)
	if err != nil {
		return nil, nil, err
	}

	pos := m.NewPosition()

	for _, param := range params {
		value := param.Values[0]
		switch param.Name {
		case "id":
			ds := m.Session(value)
			if ds == nil {
				return badRequest, nil, nil
			}
			pos.DeviceID = ds.DeviceID
		case "gprmc":
			parser := kit.NewParser(pattern, value)
			if !parser.Matches() {
				return badRequest, nil, nil
			}

			date := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

			pos.Valid = parser.Next() == "A"
			pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
			pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
			pos.Speed = parser.MustDouble()
			pos.Course = parser.NextDoubleDefault(0)

			date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
			pos.SetTime(date.Date())
		case "alt":
			pos.Altitude = kit.Double(value)
		case "batt":
			pos.Set(model.KeyBatteryLevel, kit.Double(value))
		}
	}

	if pos.DeviceID != 0 {
		return ok, []*model.Position{pos}, nil
	}
	return badRequest, nil, nil
}
