package apel

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// ApelProtocolDecoderTest has no recorded calls: its vectors are commented out. They
// are replayed here as a smoke test.
func TestVectors(t *testing.T) {
	env := oracle.NewEnv()
	d := &Decoder{}
	run := func(h string) any {
		return d.Decode(env.Message(oracle.Record{}), kit.Hex(h))
	}
	if r := run("0c002900f12a00000f003235303032363533343135313036340f0033353638393530333632373938313101002000000000"); r != nil {
		t.Fatalf("id: %v", r)
	}
	r := run("5c00380046e6a95136b693277f11b41a00172709f2ff03160002b9bc630007000000000000000000000000000000c31071090000880500000000000000000000")
	if p := r.([]*model.Position); len(p) != 1 || !p[0].Valid {
		t.Fatalf("gps: %v", r)
	}
	run("7900040069ea030000000000")
	r = run("8300c20003006aea03005c003800223aab5107a393276617b41a0030d506e3000414010250bf630007000000000000000000000000000000c3107209000089050000000000006bea03005c003800403aab5107a393276617b41a0030d506e3000414010250bf630007000000000000000000000000000000c3107209000089050000000000006cea03005c0038005e3aab5107a393276617b41a0030d506e3000414010250bf630007000000000000000000000000000000c31072090000890500000000000000000000")
	if p := r.([]*model.Position); len(p) != 3 {
		t.Fatalf("log: %v", r)
	}
}
