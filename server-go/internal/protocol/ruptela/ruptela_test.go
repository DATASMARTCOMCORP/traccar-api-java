package ruptela

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "RuptelaProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "RuptelaProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
