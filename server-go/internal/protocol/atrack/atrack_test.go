package atrack

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	const form1 = "%AT%BV%MV%SA%VN%PD%IA%MP%EL%ET%FC%FL%RP%ML%MF%TR%EH%DL%EG%HA%HB%HC%IP%MT"
	const form2 = "%AT%BV%CD%CE%CM%CN%DT%GN%GQ%GS%GV%LC%ME%MV%RL%SA%SM%CS%HT%VN%PD%IA%MP%EL%ET%FC%FL%RP%ML%MF%TR%EH%CR%DL%EG%HA%HB%HC%IP%MT%PF"
	oracle.Run(t, "AtrackProtocolDecoderTest", oracle.Handlers{
		Decoder: func(method string, env *oracle.Env) ingest.Decoder {
			opts, err := ReadOptions(env.Cfg, "unknown")
			if err != nil {
				t.Fatal(err)
			}
			return &Decoder{Options: opts}
		},
		// The settings the Java test changes between calls.
		Before: func(r oracle.Record, dec ingest.Decoder) {
			d := dec.(*Decoder)
			form := func(s string) { d.Form = &s }
			switch r.Method {
			case "testDecode":
				d.LongDate = r.Line > 37 && r.Line < 42
				d.Custom = r.Line > 44 && r.Line < 94
			case "testDecodeCustom":
				d.Custom = true
				if r.Line > 132 {
					form(form2)
				} else {
					form(form1)
				}
			case "testDecodeBeacon":
				d.Custom, d.FrameMask = true, 1
				form("%BC")
			}
		},
	})
}

func TestJavaFramer(t *testing.T) {
	oracle.Run(t, "AtrackFrameDecoderTest", oracle.Handlers{
		Framer: func(string) ingest.Framer { return Framer() },
	})
}
