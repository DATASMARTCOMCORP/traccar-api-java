package totem

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "TotemProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return ingest.DecoderFunc(decode) },
	})
}

// TotemProtocolEncoderTest compares with assertEquals, so it has no recorded calls.
func TestEncoder(t *testing.T) {
	command := func() *model.Command {
		c := &model.Command{DeviceID: 2, Type: model.CommandRebootDevice}
		c.Attributes.Set(model.CommandKeyDevicePassword, "000000")
		return c
	}
	m := oracle.NewEnv().Message(oracle.Record{})
	if got := encode(m, command()); got != "$$0020CF000000,0061D" {
		t.Errorf("encode: %v", got)
	}
	if got := SMSEncoder.Encode(m, command()); got != "*000000,006#" {
		t.Errorf("SMS encode: %v", got)
	}
}
