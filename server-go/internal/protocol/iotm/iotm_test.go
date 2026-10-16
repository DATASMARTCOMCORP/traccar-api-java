package iotm

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// mqtt decodes the recorded MQTT bytes with the connection's codec, then the message.
func mqtt(codec *ingest.MQTT, decoder ingest.Decoder) ingest.Decoder {
	return ingest.DecoderFunc(func(m *ingest.Message, msg any) any {
		message := codec.Frame(m, msg.(*kit.Buf))
		if message == nil {
			panic("incomplete MQTT message")
		}
		return decoder.Decode(m, message)
	})
}

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "IotmProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder {
			codec := ingest.NewMQTT()
			return mqtt(codec, ingest.MQTTDecoder(codec, decode))
		},
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "IotmProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return Encoder(ingest.NewMQTT()) },
	})
}
