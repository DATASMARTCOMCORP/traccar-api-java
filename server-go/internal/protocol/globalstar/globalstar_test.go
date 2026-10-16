package globalstar

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// testDecoder is the HTTP decoder adapted for the oracle, keeping the Go decoder at hand.
type testDecoder struct {
	ingest.Decoder
	d *Decoder
}

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "GlobalstarProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder {
			d := &Decoder{}
			return testDecoder{oracle.HTTPDecoder(d), d}
		},
		// decoder.setModelOverride("AtlasTrax") at line 18, back to null at line 31.
		Before: func(r oracle.Record, dec ingest.Decoder) {
			d := dec.(testDecoder).d
			d.ModelOverride = nil
			if r.Line > 18 && r.Line < 31 {
				atlas := "AtlasTrax"
				d.ModelOverride = &atlas
			}
		},
	})
}
