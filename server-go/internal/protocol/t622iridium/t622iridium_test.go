package t622iridium

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "T622IridiumProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		Before: func(_ oracle.Record, d ingest.Decoder) {
			format := "01,02,03,04,05,08"
			d.(*Decoder).Format = &format
		},
	})
}
