package xirgo

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "XirgoProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
		// decoder.setForm(...) at lines 13 and 24 of testDecodeCustom.
		Before: func(r oracle.Record, d ingest.Decoder) {
			if r.Method != "testDecodeCustom" {
				return
			}
			form := "UID,EV,D,T,LT,LN,AL,GSPT,HD,SV,HP,BV,CQ,GS,SI,IG,OT"
			if r.Line > 24 {
				form = "UID,EV,D,T,LT,LN,AL,GSPT,SV,HP,BV,CQ,MI,GS,SI,IG,OT"
			}
			d.(*Decoder).Form = &form
		},
	})
}

func TestJavaEncoder(t *testing.T) {
	oracle.Run(t, "XirgoProtocolEncoderTest", oracle.Handlers{
		Encoder: func(string, ingest.Decoder) ingest.CommandEncoder { return ingest.CommandEncoderFunc(encode) },
	})
}
