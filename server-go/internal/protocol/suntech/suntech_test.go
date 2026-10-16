package suntech

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "SuntechProtocolDecoderTest", oracle.Handlers{
		Decoder: func(method string, _ *oracle.Env) ingest.Decoder {
			d := &Decoder{}
			switch method {
			case "testDecodeTemperature":
				d.HBM, d.IncludeADC = 1, true
			case "testDecodeRpm":
				d.HBM, d.IncludeRPM = 1, true
			case "testDecodeHours":
				d.HBM = 1
			}
			return d
		},
		// The settings the Java test changes between calls.
		Before: func(r oracle.Record, d ingest.Decoder) {
			s := d.(*Decoder)
			switch {
			case r.Method == "testDecode" && r.Line > 216:
				s.ProtocolType = 1
				if r.Line > 235 {
					s.HBM = 2
				}
			case r.Method == "testDecodeTemperature" && r.Line > 266:
				s.IncludeTemp = true
			case r.Method == "testDecodeDriver" && r.Line > 323:
				s.HBM = 1
				if r.Line > 329 {
					s.HBM = 2
				}
			}
		},
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "SuntechFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "SuntechProtocolEncoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		Encoder: func(_ string, d ingest.Decoder) ingest.CommandEncoder {
			e := &Encoder{}
			if d != nil {
				e.Decoder = d.(*Decoder)
			}
			return e
		},
	})
}
