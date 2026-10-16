package osmand

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// The Java tests ran with the JVM zone America/Sao_Paulo.
func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "OsmAndProtocolDecoderTest", oracle.Handlers{
		Decoder: func(_ string, env *oracle.Env) ingest.Decoder {
			d, err := New(env.Cfg, javatime.TimeZone("America/Sao_Paulo"))
			if err != nil {
				t.Fatal(err)
			}
			return oracle.HTTPDecoder(d)
		},
	})
}
