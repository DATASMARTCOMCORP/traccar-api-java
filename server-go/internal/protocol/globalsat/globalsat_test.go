package globalsat

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "GlobalSatProtocolDecoderTest", oracle.Handlers{
		Decoder: func(_ string, env *oracle.Env) ingest.Decoder { return NewDecoder(env.Cfg, "unknown") },
		// decoder.setFormat0(...) calls of the Java test.
		Before: func(r oracle.Record, dec ingest.Decoder) {
			d := dec.(*Decoder)
			switch {
			case r.Line > 46:
				d.Format0 = "SPRXYAB27GHKLMmnaefghiotuvwb*U!"
			case r.Line > 29:
				d.Format0 = "TSPRXAB27GHKLMnaicz*U!"
			case r.Line > 21:
				d.Format0 = "SORPZAB27GHKLMN*U!"
			case r.Line > 16:
				d.Format0 = "TSPRXAB27GHKLMmnaictuvw*U!"
			}
		},
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "GlobalSatProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
