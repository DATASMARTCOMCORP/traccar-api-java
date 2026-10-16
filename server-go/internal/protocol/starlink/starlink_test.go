package starlink

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// formats are the decoder.setFormat(...) calls of the Java test, by line.
var formats = []struct {
	line   int
	format string
}{
	{14, "#EDT#,#EID#,#PDT#,#LAT#,#LONG#,#SPD#,#HEAD#,#ODO#,#ALT#,#SATU#,#CSS#,#IGN#,#VIN#,#VBAT#,#TV1#,#TS1#,#TV2#,#TS2#,#TV3#,#TS3#,#TV4#,#TS4#,#V4#"},
	{19, "#IMEI#,#EDT#,#PDT#,#LAT#,#LONG#,#SPD#,#IGN#,#ODO#,#DUR#,#TDUR#,#LAC#,#CID#,#VIN#,#VBAT#,#EID#,#EDSC#,#DRV#,#SATU#,#CSS#,#OUT1#,#OUT2#,#CFL#"},
	{25, "#IMEI#,#EDT#,#EDSC#,#EID#,#PDT#,#LAT#,#LONG#,#SPDK#,#IGNL#,#HEAD#,#ODO#,#DUR#,#TDUR#,#VIN#,#VBAT#,#BATC#,#SATU#,#CSS#,#IN2#,#TVI#,#OUT1#,#OUT2#,#OUT3#,#OUTC#"},
	{30, "#IMEI#,#EDT#,#PDT#,#LAT#,#LONG#,#SPD#,#IGN#,#ODO#,#DUR#,#TDUR#,#LAC#,#CID#,#VIN#,#VBAT#,#EID#,#EDSC#,#DRV#,#SATU#,#CSS#,#OUT1#,#OUT2#,#IN2#,#IND#"},
	{36, "#EDT#,#EID#,#PDT#,#LAT#,#LONG#,#SPD#,#HEAD#,#ODO#,#LAC#,#CID#,#VIN#,#VBAT#,#TI1#,#TS1#,#TV1#,#TH1#,#TD1#,#EDSC#,#TI2#,#TS2#,#TV2#,#TH2#,#TD2#"},
	{42, "#IMEI#,#EDT#,#PDT#,#LAT#,#LONG#,#SPD#,#IGN#,#ODO#,#DUR#,#TDUR#,#LAC#,#CID#,#VIN#,#VBAT#,#EID#,#EDSC#,#DRV#,#SATU#,#CSS#,#OUT1#,#OUT2#"},
	{48, defaultFormat},
	{89, "#EDT#,#EID#,#PDT#,#LAT#,#LONG#,#SPD#,#HEAD#,#ODO#,#LAC#,#CID#,#VIN#,#VBAT#"},
}

func TestJavaDecoder(t *testing.T) {
	applied := 0
	oracle.Run(t, "StarLinkProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder {
			applied = 0
			return NewDecoder(defaultFormat, defaultDateFormat)
		},
		Before: func(r oracle.Record, d ingest.Decoder) {
			for applied < len(formats) && formats[applied].line < r.Line {
				d.(*Decoder).Format = formats[applied].format
				applied++
			}
		},
	})
}
