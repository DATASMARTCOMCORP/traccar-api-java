package xt2400

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// configs are the decoder.setConfig(...) calls of the Java test, by line; the formats
// accumulate, as in the Java decoder.
var configs = []struct {
	line   int
	config string
}{
	{13, "\n:wycfg pcr[1] 012801030405060708090a1213c8545657585a656e7d2cd055595d5e71797a7b7c7e7f80818285866b\n"},
	{18, "\n:wycfg pcr[0] 001001030406070809570a13121714100565\n"},
	{23, "\n:wycfg pcr[0] 000f01030406070809570a131217141005\n"},
}

func TestJavaDecoder(t *testing.T) {
	applied := 0
	oracle.Run(t, "Xt2400ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		Before: func(r oracle.Record, d ingest.Decoder) {
			for applied < len(configs) && configs[applied].line < r.Line {
				d.(*Decoder).SetConfig(configs[applied].config)
				applied++
			}
		},
	})
}

func TestUnknownTag(t *testing.T) {
	d := &Decoder{}
	d.SetConfig(":wycfg pcr[0] 0002ff\n")
	var err error
	func() {
		defer kit.Recover(&err)
		d.Decode(oracle.NewEnv().Message(oracle.Record{}), kit.Hex("00"))
	}()
	if err == nil || err.Error() != "IllegalArgumentException: Unknown tag: 0xFF" {
		t.Errorf("got %v", err)
	}
}
