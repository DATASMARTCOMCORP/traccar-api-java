package nvs

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "NvsProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// NvsFrameDecoderTest asserts with assertEquals, so its cases are copied here.
func TestJavaFramer(t *testing.T) {
	oracle.FrameCases(t, Framer(),
		[2]string{"0012333537303430303630303137383234312e38", "0012333537303430303630303137383234312e38"},
		[2]string{"cccccccc0073000144b9ddf2aca002015694823d1f165d80902139a44f00aa001e1400000103000a080115001a001d001e0141004001f00065001301061600001700001800004231da430000440000085000000000480000000049000000004a0000000047ffffffff6900000004c700000000e10000000100954a",
			"cccccccc0073000144b9ddf2aca002015694823d1f165d80902139a44f00aa001e1400000103000a080115001a001d001e0141004001f00065001301061600001700001800004231da430000440000085000000000480000000049000000004a0000000047ffffffff6900000004c700000000e10000000100954a"})
}
