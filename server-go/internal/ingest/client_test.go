package ingest

import (
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

func TestHTTPResponseFramer(t *testing.T) {
	f := HTTPResponseFramer(64 * 1024)
	stream := "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello" +
		"HTTP/1.1 404 Not Found\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n2;x=1\r\nde\r\n0\r\n\r\n" +
		"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n" +
		"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort"
	in := kit.NewBuf([]byte(stream))
	want := []HTTPResponse{{200, []byte("hello")}, {404, []byte("abcde")}, {204, []byte{}}}
	for i, w := range want {
		got, ok := f.Frame(nil, in).(*HTTPResponse)
		if !ok || got.Status != w.Status || string(got.Body) != string(w.Body) {
			t.Fatalf("response %d: %+v, want %+v", i, got, w)
		}
	}
	if got := f.Frame(nil, in); got != nil {
		t.Fatalf("incomplete response decoded: %+v", got)
	}
	if string(in.Bytes()) != "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort" {
		t.Fatalf("incomplete response consumed: %q", in.Bytes())
	}
}
