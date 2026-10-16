package carcell

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// The Java protocol has no tests: this only checks that the pattern compiles and parses a
// report of each shape.
func TestDecodeSamples(t *testing.T) {
	samples := []string{
		"$123456789012345,N2345.1234,W04612.1234,50,180,+001-002+003,100,20,0,1,G,010120,123000,0,1,0,A0,1250,AB",
		"%123456789012345,CEL,S23.5,W46.2,50,180,10,100,20,0,1,C,010120,123000,1,0,05,F1,1250,12345678901234567890,AB",
	}
	for _, s := range samples {
		env := oracle.NewEnv()
		m := env.Message(oracle.Record{Now: 1})
		if decode(m, s) == nil {
			t.Errorf("no position for %q", s)
		}
	}
}
