package xexun

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJava(t *testing.T) {
	handlers := oracle.Handlers{
		Decoder: func(method string, _ *oracle.Env) ingest.Decoder {
			return &Decoder{Full: method == "testDecodeFull"}
		},
		Framer: func(string) ingest.Framer { return Framer() },
	}
	for _, test := range []string{"XexunProtocolDecoderTest", "XexunFrameDecoderTest"} {
		oracle.Run(t, test, handlers)
	}
}
