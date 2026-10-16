package mta6

import (
	"bytes"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// There is no Java test of this protocol: this checks the framing of a request and the
// reply on a synthetic packet.
func TestDecode(t *testing.T) {
	body := append([]byte("id=123456&bin="), kit.ParseHex(
		"31"+"05"+"01"+"00"+"00"+ // header: packet id, offset, count, reserved, timezone
			"80"+"00"+ // flags (battery), event
			"00000000"+"00000000"+"00000000"+"0001"+ // latitude, longitude, time, week
			"0c00"+"14"+"23")...) // power, temperature, satellites
	run := func(d *Decoder) []*model.Position {
		var got []*model.Position
		dec := oracle.HTTPDecoder(d)
		m := ingest.NewMessage(t.Context(), oracle.NewEnv(), "", kit.CalendarDate(2024, 1, 1, 0, 0, 0, 0))
		out := dec.Decode(m, &oracle.HTTPRequest{Method: "POST", URI: "/", Body: body})
		if out != nil {
			got = out.([]*model.Position)
		}
		return got
	}
	positions := run(&Decoder{})
	if len(positions) != 1 || !sats(positions[0]) || !positions[0].Valid {
		t.Fatalf("format A: %d %s", len(positions), positions[0].Attributes.String())
	}
	resp, _, err := (&Decoder{}).DecodeHTTP(t.Context(), ingest.NewMessage(t.Context(), oracle.NewEnv(), "", kit.CalendarDate(2024, 1, 1, 0, 0, 0, 0)), nil, body)
	if err != nil || !bytes.Equal(resp.Body, []byte("#ACK#\x31\x01\x00")) {
		t.Fatalf("response %v %v", resp, err)
	}
}

func sats(p *model.Position) bool {
	return p.Attributes.String() == `{"battery":3,"power":0,"deviceTemp":20,"rssi":2,"sat":3}`
}
