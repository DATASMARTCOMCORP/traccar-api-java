package cityeasy

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "CityeasyProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "CityeasyProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
