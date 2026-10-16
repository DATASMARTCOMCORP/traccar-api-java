package its

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "ItsProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "ItsFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

// ItsProtocolEncoderTest uses assertEquals, so it has no recorded calls.
func TestJavaEncoder(t *testing.T) {
	c := &model.Command{DeviceID: 1, Type: model.CommandEngineStop}
	if got := encode(nil, c); got != "@SET#RLP,OP1," {
		t.Errorf("engineStop: %v", got)
	}
}

func TestJavaEncoderRecorded(t *testing.T) {
	oracle.Run(t, "ItsProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
