// Package spot is the SPOT protocol (SpotProtocol): Globalstar SPOT XML message lists
// posted over HTTP.
package spot

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "spot",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is SpotProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	root := kit.ParseXML(body)

	positions = []*model.Position{}
	for _, n := range root.Find("messageList", "message") {
		ds := m.Session(n.Child("esnName"))
		if ds == nil {
			continue
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Valid = true
		t, err := javatime.ParseISOZoned(n.Child("timestamp"))
		if err != nil {
			kit.Throw("DateTimeParseException: %v", err)
		}
		pos.SetTime(t)
		pos.SetLatitude(kit.Double(n.Child("latitude")))
		pos.SetLongitude(kit.Double(n.Child("longitude")))

		pos.Set(model.KeyEvent, n.Child("messageType"))

		positions = append(positions, pos)
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, positions, nil
}
