package eseal

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "EsealProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "EsealProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}

func TestProtocol(t *testing.T) {
	p, err := Protocol(oracle.NewEnv().Cfg)
	if err != nil || p.Name != "eseal" || p.Servers[0].NewPipeline().Decoder.(*Decoder).Config != nil {
		t.Fatalf("Protocol: %+v, %v", p, err)
	}
}
