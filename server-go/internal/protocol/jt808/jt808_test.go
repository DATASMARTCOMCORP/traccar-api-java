package jt808

import (
	"encoding/hex"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "Jt808ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		// decoder.setModelOverride(...) between calls.
		Before: func(r oracle.Record, d ingest.Decoder) {
			s := d.(*Decoder)
			switch {
			case r.Line > 409:
				s.ModelOverride = "C5L"
			case r.Line > 391:
				s.ModelOverride = "JC371"
			case r.Line > 385:
				s.ModelOverride = "AL300"
			case r.Line > 375:
				s.ModelOverride = "MV710G"
			default:
				s.ModelOverride = ""
			}
		},
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "Jt808FrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

// identity checks the encoder tests' verifyFrame(expected, encoded) records, which hold
// only the expected bytes; the encoded output itself is checked by the encode records.
func identity(string) ingest.Framer {
	return ingest.FramerFunc(func(_ *ingest.Message, b *kit.Buf) any { return b.ReadSlice(b.ReadableBytes()) })
}

func TestJavaEncoder(t *testing.T) {
	calls := map[string]int{}
	oracle.Run(t, "Jt808ProtocolEncoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		Encoder: func(method string, d ingest.Decoder) ingest.CommandEncoder {
			calls[method]++
			e := &Encoder{Decoder: d.(*Decoder)}
			switch {
			case method == "testEncodeJimiCustom":
				e.ModelOverride = "JC371"
			case method == "testEncode" && calls[method] > 2:
				e.ModelOverride = "BSJ" // encoder.setModelOverride("BSJ") before the third call
			}
			return e
		},
		Framer: identity,
	})
}

// TestJavaFrameEncoder replays Jt808FrameEncoderTest: the frame encoder applied to the
// test's input (the first buffer it built) gives the recorded frame.
func TestJavaFrameEncoder(t *testing.T) {
	recs, err := oracle.Records("Jt808FrameEncoderTest")
	if err != nil || len(recs) == 0 {
		t.Fatalf("no records: %v", err)
	}
	for _, r := range recs {
		in, err := hex.DecodeString(r.Created[0])
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(FrameEncode(in)); got != *r.Output.Bytes {
			t.Errorf("%s:%d: got %s, Java %s", r.Method, r.Line, got, *r.Output.Bytes)
		}
	}
	t.Logf("Jt808FrameEncoderTest: replayed %d", len(recs))
}
