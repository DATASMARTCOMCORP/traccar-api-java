package xrb28

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "Xrb28ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "Xrb28ProtocolEncoderTest", oracle.Handlers{
		// encodeCommand(null, command): no channel, so no decoder.
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return &Encoder{} },
	})
}

func TestLatin1(t *testing.T) {
	if got := string(latin1([]byte("ÿÿ*SCOS€"))); got != "\xff\xff*SCOS?" {
		t.Errorf("got %q", got)
	}
}
