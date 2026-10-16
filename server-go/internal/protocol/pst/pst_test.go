package pst

import (
	"encoding/hex"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "PstProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "PstFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "PstProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}

// TestJavaFrameEncoder replays PstFrameEncoderTest: the encoder's input is the buffer
// the test built before the expected frame.
func TestJavaFrameEncoder(t *testing.T) {
	recs, err := oracle.Records("PstFrameEncoderTest")
	if err != nil || len(recs) == 0 {
		t.Fatalf("no records: %v", err)
	}
	for _, r := range recs {
		in, err := hex.DecodeString(r.Created[len(r.Created)-2])
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(FrameEncode(in)); got != *r.Output.Bytes {
			t.Errorf("%s:%d: got %s, Java %s", r.Method, r.Line, got, *r.Output.Bytes)
		}
	}
	t.Logf("PstFrameEncoderTest: replayed %d", len(recs))
}
