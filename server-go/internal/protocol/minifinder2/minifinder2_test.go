package minifinder2

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "Minifinder2ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "Minifinder2ProtocolEncoderTest", oracle.Handlers{
		Encoder: func(method string, _ ingest.Decoder) ingest.CommandEncoder {
			e := &Encoder{}
			if method == "testEncodeNano" {
				nano := "Nano" // encoder.setModelOverride("Nano")
				e.ModelOverride = &nano
			}
			return e
		},
	})
}
