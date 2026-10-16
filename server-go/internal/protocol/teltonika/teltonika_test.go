package teltonika

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJava(t *testing.T) {
	handlers := oracle.Handlers{
		Decoder: func(method string, _ *oracle.Env) ingest.Decoder {
			return &Decoder{Connectionless: method == "testDecodeConnectionless"}
		},
		Before: func(r oracle.Record, d ingest.Decoder) {
			dec := d.(*Decoder)
			if r.Method == "testDecode" && r.Line > 150 { // decoder.setModelOverride("FMB130")
				model := "FMB130"
				dec.ModelOverride = &model
			}
			if r.Method == "testDecode" && r.Line > 156 { // decoder.setExtended(true)
				dec.Extended = true
			}
		},
		Framer:  func(string) ingest.Framer { return Framer() },
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	}
	for _, test := range []string{"TeltonikaProtocolDecoderTest", "TeltonikaFrameDecoderTest", "TeltonikaProtocolEncoderTest"} {
		oracle.Run(t, test, handlers)
	}
}
