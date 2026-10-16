package meitrack

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "MeitrackProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "MeitrackProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}

// MeitrackFrameDecoderTest checks with assertEquals, so it has no recorded calls: its
// vector is here.
func TestFramer(t *testing.T) {
	const frame = "24244e3132372c3836333037313031333830333036362c4141412c33352c2d312e3330323638302c33362e3835323133352c3135303430393231313032362c412c392c302c302e312c302c352c313635332c343039362c33323634382c3633397c30327c313030347c3930432c303030302c307c307c307c3346467c3330302c2a37430d0a"
	got := Framer().Frame(nil, kit.Hex(frame))
	if got == nil || got.(*kit.Buf).HexDump() != frame {
		t.Errorf("frame: got %v", got)
	}
}
