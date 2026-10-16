package xexun2

import (
	"encoding/hex"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "Xexun2ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(Decode) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "Xexun2FrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "Xexun2ProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}

// TestJavaFrameEncoder replays Xexun2FrameEncoderTest: the frame encoder applied to the
// test's input gives the recorded frame.
func TestJavaFrameEncoder(t *testing.T) {
	recs, err := oracle.Records("Xexun2FrameEncoderTest")
	if err != nil || len(recs) == 0 {
		t.Fatalf("no records: %v", err)
	}
	for _, r := range recs {
		// Created holds buffers of earlier tests too: the input is the one before the expected frame.
		in, err := hex.DecodeString(r.Created[len(r.Created)-2])
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(FrameEncode(in)); got != *r.Output.Bytes {
			t.Errorf("%s:%d: got %s, Java %s", r.Method, r.Line, got, *r.Output.Bytes)
		}
	}
	t.Logf("Xexun2FrameEncoderTest: replayed %d", len(recs))
}

// TestFrameUnescape repeats the escaped-frame check of Xexun2FrameDecoderTest.
func TestFrameUnescape(t *testing.T) {
	got := Framer().Frame(nil, kit.Hex("FAAF123456FBBF01123456FBBF02123456FAAF")).(*kit.Buf)
	if got.HexDump() != "faaf123456faaf123456fbbf123456faaf" {
		t.Errorf("got %s", got.HexDump())
	}
}
