package orbcomm

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "OrbcommProtocolDecoderTest", oracle.Handlers{
		Decoder: func(string, *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}

func TestRequest(t *testing.T) {
	p := &Poller{}
	got := string(p.request("id 1", true, "p@ss", true, "gw.example"))
	want := "GET /GLGW/2/RestMessages.svc/JSON/get_return_messages/?access_id=id%201&password=p%40ss&start_utc=0001-01-01%2000%3A00%3A00 HTTP/1.1\r\nhost: gw.example\r\ncontent-length: 0\r\n\r\n"
	if got != want {
		t.Errorf("request\n%q\nwant\n%q", got, want)
	}
}
