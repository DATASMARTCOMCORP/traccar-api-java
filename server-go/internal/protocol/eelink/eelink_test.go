package eelink

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "EelinkProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "EelinkProtocolEncoderTest", oracle.Handlers{
		Encoder: func(method string, _ ingest.Decoder) ingest.CommandEncoder {
			return &Encoder{Connectionless: method == "testEncodeUdp"}
		},
	})
}
