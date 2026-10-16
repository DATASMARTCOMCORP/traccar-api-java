package bce

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "BceProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "BceFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "BceProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
