package techtocruz

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "TechtoCruzProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// TestFramer ports TechtoCruzFrameDecoderTest (its calls are not recorded).
func TestFramer(t *testing.T) {
	for _, s := range []string{
		"$$A35,RESPO|G33|8612345678910|CRUZ,*E3",
		"$$A120,8612345678910,211005105836,A,FLEX,KCB 947C,000.0,0,-1.38047,S,36.93951,E,1648.4,243.140,21,28,12.1,3.7,0,1,0,0,0,*F6",
	} {
		got := Framer().Frame(nil, kit.NewBuf([]byte(s)))
		if b, ok := got.(*kit.Buf); !ok || string(b.Bytes()) != s {
			t.Errorf("frame of %s: got %v", s, got)
		}
	}
}
