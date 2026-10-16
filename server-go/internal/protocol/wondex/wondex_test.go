package wondex

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJava(t *testing.T) {
	handlers := oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	}
	for _, test := range []string{"WondexProtocolDecoderTest", "WondexProtocolEncoderTest"} {
		oracle.Run(t, test, handlers)
	}
}

// TestFrame is WondexFrameDecoderTest (plain assertions, not recorded).
func TestFrame(t *testing.T) {
	oracle.FrameCases(t, Framer(),
		[2]string{"f0d70b0001ca9a3b", ""},
		[2]string{
			"313034343938393630312c32303133303332333039353531352c31332e3537323737362c35322e3430303833382c302c3030302c37322c302c320d0a",
			"313034343938393630312c32303133303332333039353531352c31332e3537323737362c35322e3430303833382c302c3030302c37322c302c32",
		},
		[2]string{"d0d70b0001ca9a3b", "d0d70b0001ca9a3b"},
	)
}
