package genx

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	const long = "1,2,3,4,13,17,10,23,27,11,7,8,46,56,59,70,74,75,77,89,90,93,99,107,112,113,114,176,175,178,181,182"
	oracle.Run(t, "GenxProtocolDecoderTest", oracle.Handlers{
		Decoder: func(_ string, env *oracle.Env) ingest.Decoder {
			columns, err := ReadColumns(env.Cfg, "unknown")
			if err != nil {
				t.Fatal(err)
			}
			return &Decoder{ReportColumns: columns}
		},
		// decoder.setReportColumns(...) at lines 13, 18 and 26.
		Before: func(r oracle.Record, d ingest.Decoder) {
			g := d.(*Decoder)
			switch {
			case r.Line > 26:
				g.ReportColumns = ParseColumns(long)
			case r.Line > 18:
				g.ReportColumns = ParseColumns("1,2,3,4")
			default:
				g.ReportColumns = ParseColumns("28" + long[1:])
			}
		},
	})
}
