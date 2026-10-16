// Package valtrack is the Valtrack protocol (ValtrackProtocol): JSON position lists posted
// over HTTP.
package valtrack

import (
	"context"
	"net/http"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "valtrack",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is ValtrackProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes the "resource" array of a request; it answers 200. Errors are where
// the Java decoder throws (malformed JSON, missing or mistyped fields).
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)

	object := kit.ParseJSONObject(body)
	messages := object.Array("resource")
	if messages == nil {
		kit.Throw("NullPointerException: resource")
	}

	positions = []*model.Position{}
	for _, v := range messages {
		message := kit.JSONObjectOf(v)

		ds := m.Session(message.String("devid"))
		if ds == nil {
			continue
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Valid = true
		pos.SetTime(m.Now)
		pos.SetLatitude(kit.Double(message.String("lat")))
		pos.SetLongitude(kit.Double(message.String("lon")))
		if speed := message.String("speed"); speed != "" {
			pos.Speed = kit.Double(speed)
		}

		pos.Set(model.KeyBattery, kit.Double(message.String("vbat")))

		positions = append(positions, pos)
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, positions, nil
}
