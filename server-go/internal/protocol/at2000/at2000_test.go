package at2000

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "At2000FrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

// At2000ProtocolDecoderTest only runs on CI (assumeTrue), so it has no recorded calls; its
// last pair of vectors is decoded here as a smoke test of the cipher stream.
func TestDecoder(t *testing.T) {
	env := oracle.NewEnv()
	d := &Decoder{}
	run := func(h string) any { return d.Decode(env.Message(oracle.Record{}), kit.Hex(h)) }
	if r := run("01012f00000000000000000000000000003335363137333036343430373439320fad981997ae8e031fe10c0ea7641903ca32c0331df467233d2a9cd886fbeef8"); r != nil {
		t.Fatalf("device id: %v", r)
	}
	r := run("893f0000000000000000000000000000e048b1a31deba3f5dbe8877f574877e6ed4d022b6611a10d80dfc4c0c11fa8aacf4a9de61528327e2b66843dd9c5d3a7cc9ee1d9c71a34bb482145d88b4fda3e")
	positions := r.([]*model.Position)
	if len(positions) != 1 {
		t.Fatalf("positions: %v", r)
	}
	p := positions[0]
	if y := p.FixTime.Time.Year(); y < 2010 || y > 2030 || p.Latitude == 0 || p.Longitude == 0 {
		t.Fatalf("position: %+v", p)
	}
}
