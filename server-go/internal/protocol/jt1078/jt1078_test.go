package jt1078

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "Jt1078FrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}

type frame struct {
	device, channel int
	data            string
	timestamp       int64
	key             bool
	payload         int
}

type streams struct{ frames []frame }

func (s *streams) Lookup(*ingest.Message, string) (int64, bool) { return 1, true }
func (s *streams) HandleFrame(deviceID int64, channel int, data []byte, ts int64, key bool, pt int) {
	s.frames = append(s.frames, frame{int(deviceID), channel, string(data), ts, key, pt})
}
func (s *streams) RemoveStream(int64, int) {}

// TestReassembly checks the subpackage handling (Java has no decoder test).
func TestReassembly(t *testing.T) {
	packet := func(sub int, body string) *kit.Buf {
		b := kit.Empty()
		b.WriteInt(0x30316364)
		b.WriteByte8(0x81)
		b.WriteByte8(0xe2) // M + PT 98
		b.WriteShort(1)
		b.WriteBytes(kit.ParseHex("013050370310")) // id
		b.WriteByte8(1)                            // channel
		b.WriteByte8(sub)                          // I-frame, subpackage
		b.WriteLong(42)
		b.WriteInt(0)
		b.WriteShort(len(body))
		b.WriteASCII(body)
		return b
	}
	s := &streams{}
	d := &Decoder{Streams: s}
	m := oracle.NewEnv().Message(oracle.Record{})
	for _, p := range []*kit.Buf{packet(0, "whole"), packet(1, "a"), packet(3, "b"), packet(2, "c")} {
		if got := d.Decode(m, p); got != nil {
			t.Fatalf("decode returned %v", got)
		}
	}
	want := []frame{{1, 1, "whole", 42, true, 98}, {1, 1, "abc", 42, true, 98}}
	if len(s.frames) != len(want) {
		t.Fatalf("frames %v", s.frames)
	}
	for i := range want {
		if s.frames[i] != want[i] {
			t.Errorf("frame %d: %+v, want %+v", i, s.frames[i], want[i])
		}
	}
}
