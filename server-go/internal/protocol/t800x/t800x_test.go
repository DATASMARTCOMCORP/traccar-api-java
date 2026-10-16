package t800x

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "T800xProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		// decoder.setModelOverride("TLW2-2BL") at line 111.
		Before: func(r oracle.Record, d ingest.Decoder) {
			if r.Line > 111 {
				model := "TLW2-2BL"
				d.(*Decoder).ModelOverride = &model
			}
		},
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "T800xProtocolEncoderTest", oracle.Handlers{
		Encoder: func(_ string, d ingest.Decoder) ingest.CommandEncoder {
			e := &Encoder{}
			if d != nil {
				e.Decoder = d.(*Decoder)
			}
			return e
		},
	})
}
