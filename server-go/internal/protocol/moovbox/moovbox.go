// Package moovbox is the Moovbox protocol (MoovboxProtocol): XML documents of GPS
// coordinates posted over HTTP.
package moovbox

import (
	"context"
	"net/http"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "moovbox",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is MoovboxProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	root := kit.ParseXML(body)

	id, _ := root.Attr("id")
	ds := m.Session(id)
	if ds == nil {
		return nil, nil, nil
	}

	positions = []*model.Position{}
	for _, n := range root.Find("gps", "coordinates", "coordinate") {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Valid = true
		pos.SetTime(time.UnixMilli(kit.Long(n.Child("time")) * 1000))
		pos.SetLatitude(kit.Double(n.Child("longitude")))
		pos.SetLongitude(kit.Double(n.Child("latitude")))
		pos.Altitude = kit.Double(n.Child("altitude"))
		pos.Speed = kit.Double(n.Child("speed"))

		pos.Set(model.KeySatellites, kit.Int(n.Child("satellites")))

		positions = append(positions, pos)
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, positions, nil
}
