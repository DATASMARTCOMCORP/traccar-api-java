package gator

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "GatorProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// testDecodeId is a plain assertion, not a recorded call.
func TestDecodeID(t *testing.T) {
	if got := DecodeID(12, 162, 50, 134); got != "3512345006" {
		t.Errorf("DecodeID = %s", got)
	}
}

// The Java test mocks the device's unique id as "13332082112".
func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "GatorProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder {
			return ingest.CommandEncoderFunc(func(_ *ingest.Message, c *model.Command) any {
				return encodeWith(c, func() string { return "13332082112" })
			})
		},
	})
}
