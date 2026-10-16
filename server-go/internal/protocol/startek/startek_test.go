package startek

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJava(t *testing.T) {
	handlers := oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
		Framer:  func(string) ingest.Framer { return Framer() },
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	}
	for _, test := range []string{"StartekProtocolDecoderTest", "StartekFrameDecoderTest", "StartekProtocolEncoderTest"} {
		oracle.Run(t, test, handlers)
	}
}
