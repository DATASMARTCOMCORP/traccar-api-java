package navis

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "NavisProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "NavisFrameDecoderTest", oracle.Handlers{
		Framer: func(method string) ingest.Framer {
			f := &Framer{}
			if method == "testDecodeFlex10" {
				f.FlexDataSize = 73 // frameDecoder.setFlexDataSize(73)
			}
			return f
		},
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "NavisProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
