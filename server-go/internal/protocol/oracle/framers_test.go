package oracle_test

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// TestJSONFramer replays JsonFrameDecoderTest (Traccar's shared JsonFrameDecoder).
func TestJSONFramer(t *testing.T) {
	oracle.Run(t, "JsonFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return ingest.JSONFramer() },
	})
}
