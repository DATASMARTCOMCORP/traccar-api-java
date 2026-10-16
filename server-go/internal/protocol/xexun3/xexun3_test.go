package xexun3

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "Xexun3ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(Decode) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "Xexun3FrameDecoderTest", oracle.Handlers{
		// The test's LengthFieldBasedFrameDecoder(1024, 1, 2, 3, 0).
		Framer: func(string) ingest.Framer { return ingest.NewLengthFramer(1024, 1, 2, 3, 0) },
	})
}
