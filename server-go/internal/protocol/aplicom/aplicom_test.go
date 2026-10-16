package aplicom

import (
	"strings"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "AplicomProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// AplicomFrameDecoderTest compares with assertEquals, so it has no recorded calls; its
// two cases are checked here.
func TestFramer(t *testing.T) {
	const frame = "44C20146B710C158DA009500B09F7700C054CA0EA454CA0EA403BE0BF6015D706B070000142A600000000000000002434946010801000754CA0EA4000000000000008400000000000000000000000000000000300000FE00FE0000000000000000000000000000000000000000000000000000000000000000000040502035000000000000020D0000030D0000040C0000040D0000050C0000050D0000058C0000060C"
	for _, in := range []string{"33353733303030373030393233333644" + frame[2:], frame} {
		got := Framer().Frame(oracle.NewEnv().Message(oracle.Record{}), kit.Hex(in))
		if b, ok := got.(*kit.Buf); !ok || b.HexDump() != strings.ToLower(frame) {
			t.Fatalf("frame of %s: %v", in, got)
		}
	}
}
