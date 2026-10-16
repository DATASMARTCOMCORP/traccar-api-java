package meiligao

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "MeiligaoProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "MeiligaoProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}

// MeiligaoFrameDecoderTest checks with assertEquals, so it has no recorded calls: its
// vectors are here.
func TestFramer(t *testing.T) {
	const frame = "2424007b8621700151517899553233323835372e3030302c562c333632372e313835342c4e2c30313034352e323130392c452c302e30302c372c3239303131332c2c2a31347c302e307c347c303030307c303030382c303030357c303235443030303230303541374432327c30367c303030314530353527f40d0a"
	for _, c := range []struct{ in, want string }{
		{"00", ""},
		{frame, frame},
		{"00" + frame, frame},
	} {
		got := Framer().Frame(nil, kit.Hex(c.in))
		text := ""
		if got != nil {
			text = got.(*kit.Buf).HexDump()
		}
		if text != c.want {
			t.Errorf("frame of %s: got %s, want %s", c.in, text, c.want)
		}
	}
}
