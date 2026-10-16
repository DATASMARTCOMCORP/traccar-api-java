package vtfms

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "VtfmsProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// VtfmsFrameDecoderTest (its call is inside assertEquals, so not recorded).
func TestFramer(t *testing.T) {
	s := "(863071010087648,0HK44,00,000,14,2,9,,A,114946,180313,11.0244,076.9768,282,000,00000,00000,K,0000128,1,12.8,,200,2.501,,4.001,0,0,0,0,0,0,0,,)105"
	got := Framer().Frame(nil, kit.NewBuf([]byte(s)))
	if b, ok := got.(*kit.Buf); !ok || string(b.Bytes()) != s {
		t.Fatalf("frame: %v", got)
	}
}
