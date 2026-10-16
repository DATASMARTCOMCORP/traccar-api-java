package easytrack

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "EasyTrackProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		Before: func(r oracle.Record, d ingest.Decoder) {
			if r.Line > 72 { // decoder.setModelOverride("E3+4G")
				d.(*Decoder).ModelOverride = "E3+4G"
			}
		},
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "EasyTrackProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
