package sigfox

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	var current *Decoder
	oracle.Run(t, "SigfoxProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder {
			d := &Decoder{}
			current = d
			return oracle.HTTPDecoder(d)
		},
		Before: func(r oracle.Record, _ ingest.Decoder) {
			if r.Method == "testDecode" && r.Line > 49 {
				current.ModelOverride = "Amber"
			}
		},
	})
}
