// Package pathaway is the PathAway protocol (PathAwayProtocol): HTTP requests with the
// user name and a $PWS location in the query.
package pathaway

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "pathaway", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

var pattern = kit.NewPattern().
	Text("$PWS,").
	Number("d+,").           // version
	Expression("[^,]*,").    // name
	Expression("[^,]*,").    // icon
	Expression("[^,]*,").    // color
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+.?d*),").   // altitude
	Number("(-?d+.?d*),").   // speed
	Number("(-?d+.?d*),").   // course
	Any().
	Compile()

// Decoder is PathAwayProtocolDecoder.
type Decoder struct{}

// param is parameters().get(name).get(0): a missing parameter throws.
func param(params []kit.QueryParam, name string) string {
	for _, p := range params {
		if p.Name == name {
			return p.Values[0]
		}
	}
	kit.Throw("NullPointerException: no parameter %q", name)
	return ""
}

// DecodeHTTP decodes one request; Java's exceptions are panics. Java answers with a bare
// 200 response and closes the connection; here the response asks for the close.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, _ []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	params, err := kit.QueryParams(kit.Latin1([]byte(r.RequestURI)), true)
	if err != nil {
		return nil, nil, err
	}

	ds := m.Session(param(params, "UserName"))
	if ds == nil {
		return nil, nil, nil
	}

	parser := kit.NewParser(pattern, param(params, "LOC"))
	if !parser.Matches() {
		return nil, nil, nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Valid = true
	pos.SetLatitude(parser.NextDoubleDefault(0))
	pos.SetLongitude(parser.NextDoubleDefault(0))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	response := &ingest.HTTPResponse{Status: http.StatusOK, Header: http.Header{"Connection": {"close"}}}
	return response, []*model.Position{pos}, nil
}
