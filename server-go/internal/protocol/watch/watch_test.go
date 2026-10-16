package watch

import (
	"context"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// mediaEnv is the test's mocked MediaManager, whose writeFile returns "mock.amr".
type mediaEnv struct{ *oracle.Env }

func (mediaEnv) WriteMedia(string, string, []byte) string { return "mock.amr" }

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "WatchProtocolDecoderTest", oracle.Handlers{
		Decoder: func(method string, env *oracle.Env) ingest.Decoder {
			d := &Decoder{}
			if method != "testDecodeVoiceMessage" {
				return d
			}
			return ingest.DecoderFunc(func(m *ingest.Message, msg any) any {
				return d.Decode(ingest.NewMessage(m.Context(), mediaEnv{env}, m.Remote(), m.Now), msg)
			})
		},
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "WatchFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "WatchProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return &Encoder{} },
	})
}

// The encoder of a connection takes the manufacturer ("3G" sent as "SG") and the index
// flag from the decoder.
func TestEncoderFollowsDecoder(t *testing.T) {
	env := oracle.NewEnv()
	env.UniqueID = "123456789012345"
	m := ingest.NewMessage(context.Background(), env, "", time.Now())
	d := &Decoder{}
	d.Decode(m, kit.NewBuf([]byte("[3G*8800000015*0001*0002*LK]")))
	c := &model.Command{DeviceID: 1, Type: model.CommandRebootDevice}
	got := (&Encoder{Decoder: d}).Encode(m, c).(*kit.Buf)
	if want := "[SG*123456789012345*0001*0005*RESET]"; string(got.Bytes()) != want {
		t.Fatalf("got %q, want %q", got.Bytes(), want)
	}
}
