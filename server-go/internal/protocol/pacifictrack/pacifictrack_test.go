package pacifictrack

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "PacificTrackProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// testReadBitExt (assertEquals, not recorded).
func TestReadBitExt(t *testing.T) {
	if got := readBitExt(kit.NewBuf([]byte{0b10110101})); got != 0x35 {
		t.Errorf("got %#x", got)
	}
	if got := readBitExt(kit.NewBuf([]byte{0b00000010, 0b10110101})); got != 0x135 {
		t.Errorf("got %#x", got)
	}
}
