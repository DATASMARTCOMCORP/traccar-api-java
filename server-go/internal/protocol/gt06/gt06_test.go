package gt06

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	// decoder.setModelOverride(...) calls of the Java test, by line.
	overrides := []struct {
		line  int
		model string
	}{
		{573, "LW4G-4B"}, {583, "SEEWORLD"}, {589, "R11"}, {595, "NT20"},
		{601, "XT40"}, {615, "G18"}, {621, "QH302R"}, {626, "VL103"},
	}
	oracle.Run(t, "Gt06ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		Before: func(r oracle.Record, dec ingest.Decoder) {
			d := dec.(*Decoder)
			d.ModelOverride = nil
			for _, o := range overrides {
				if r.Line > o.line {
					model := o.model
					d.ModelOverride = &model
				}
			}
		},
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "Gt06FrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "Gt06ProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
