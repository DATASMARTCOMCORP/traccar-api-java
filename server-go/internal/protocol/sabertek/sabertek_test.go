package sabertek

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "SabertekProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// SabertekFrameDecoderTest checks the frame with assertEquals (not recorded).
func TestFramer(t *testing.T) {
	in := "022c3939393939393939392c332c34302c36352c372c302c312c2d32352e3738313636362c32382e3235343730322c34302c3236382c313431342c382c35353632332c030d0a"
	want := "2c3939393939393939392c332c34302c36352c372c302c312c2d32352e3738313636362c32382e3235343730322c34302c3236382c313431342c382c35353632332c"
	got := Framer().Frame(nil, kit.Hex(in))
	if b, ok := got.(*kit.Buf); !ok || b.HexDump() != want {
		t.Fatalf("frame: %v", got)
	}
}
