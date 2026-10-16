package fifotrack

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "FifotrackProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "FifotrackFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "FifotrackProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}

// The photo buffer grows as Netty's: a capacity of 64 grows to 128, then doubles.
func TestPhotoBufferCapacity(t *testing.T) {
	p := &photoBuffer{capacity: 10, maxCapacity: 1 << 30}
	p.write(make([]byte, 10))
	if p.writableBytes() != 0 {
		t.Fatalf("writable %d", p.writableBytes())
	}
	p.write(make([]byte, 60))
	if p.capacity != 128 {
		t.Fatalf("capacity %d", p.capacity)
	}
}
