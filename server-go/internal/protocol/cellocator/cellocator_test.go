package cellocator

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// CellocatorProtocolEncoderTest is @Disabled in Java and has no recorded calls.

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "CellocatorProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "CellocatorFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}
