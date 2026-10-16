package ywt

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "YwtProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(Decode) },
	})
}

func TestSynchronization(t *testing.T) {
	env := oracle.NewEnv()
	if Decode(env.Message(oracle.Record{}), "%SN,0417061042:0,0,140117041203,404") != nil {
		t.Fatal("not null")
	}
	if len(env.Sent) != 1 || env.Sent[0] != "%AT+SN=:0,0,140117041203,404" {
		t.Errorf("sent %v", env.Sent)
	}
}
