package gps056

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "Gps056ProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// TestJavaFramer has the vectors of Gps056FrameDecoderTest, which calls the decoder with
// assertEquals (not verifyFrame), so the oracle has no records of it.
func TestJavaFramer(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"242435314750534c5f30323836323436323033333738323934361905110f160b0b7710584e1cbd1b9b4500005b100300fb0a071700ffff230030",
			"242435314750534c5f30323836323436323033333738323934361905110f160b0b7710584e1cbd1b9b4500005b100300fb0a071700ffff23"},
		{"242432354c4f474e5f3131383632343632303333373832393436322e3123",
			"242432354c4f474e5f3131383632343632303333373832393436322e3123"},
	} {
		frame := Framer().Frame(nil, kit.Hex(c.in))
		if got := frame.(*kit.Buf).HexDump(); got != c.want {
			t.Errorf("frame %s\n\tgot  %s\n\tJava %s", c.in, got, c.want)
		}
	}
}
