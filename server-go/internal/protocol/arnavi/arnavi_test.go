package arnavi

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaBinaryDecoder(t *testing.T) {
	oracle.Run(t, "ArnaviBinaryProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decodeBinary) },
	})
}

func TestJavaTextDecoder(t *testing.T) {
	oracle.Run(t, "ArnaviTextProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decodeText) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "ArnaviFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}
