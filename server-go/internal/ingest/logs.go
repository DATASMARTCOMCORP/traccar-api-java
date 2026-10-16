package ingest

import (
	"encoding/hex"
	"net"
	"strings"
	"sync"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// logRead is StandardLoggingHandler.channelRead's ConnectionManager.updateLog: a raw read,
// after the protocol handled it (so a session it opened counts), goes to the sockets of
// the users who see the endpoint's device. Without a session, the unknown unique id the
// endpoint reported, if any, goes to every socket (web.showUnknownDevices).
func (p *Processor) logRead(conn *Conn, remote string, data []byte) {
	if p.live == nil || len(data) == 0 {
		return
	}
	r := model.LogRecord{Protocol: conn.protocol, Data: p.logData(data), Host: hostString(remote)}
	s := p.sessions
	ep := endpoint{conn: conn, remote: remote}
	s.mu.Lock()
	var ds *DeviceSession
	if es := s.byEndpoint[ep]; es != nil {
		ds = es.first()
	}
	unknown, isUnknown := s.unknown[ep]
	s.mu.Unlock()
	if ds != nil {
		r.UniqueID, r.DeviceID = &ds.UniqueID, ds.DeviceID
		p.live.LogDevice(r)
		return
	}
	if isUnknown {
		r.UniqueID = &unknown.uniqueID
		p.live.LogUnknown(r)
	}
}

// logHTTPReads logs an HTTP connection's reads so far, after the request they carried was
// handled (or when it closes).
func (p *Processor) logHTTPReads(conn *Conn, remote string) {
	if conn.rec == nil {
		return
	}
	for _, data := range conn.rec.take() {
		p.logRead(conn, remote, data)
	}
}

// logData is the record's data: hex, or with logger.decodeTextData the text of a printable
// message with CR and LF escaped.
func (p *Processor) logData(data []byte) string {
	buf := kit.NewBuf(data)
	if p.textLogs && kit.IsPrintable(buf, len(data)) {
		text := kit.Latin1(data)
		return strings.ReplaceAll(strings.ReplaceAll(text, "\r", `\r`), "\n", `\n`)
	}
	return hex.EncodeToString(data)
}

// recordingConn keeps an HTTP connection's raw reads until the request they carried has
// been handled, so they're logged in Java's order.
type recordingConn struct {
	net.Conn
	mu    sync.Mutex
	reads [][]byte
}

func (c *recordingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.mu.Lock()
		c.reads = append(c.reads, append([]byte(nil), b[:n]...))
		c.mu.Unlock()
	}
	return n, err
}

// take returns and forgets the reads so far.
func (c *recordingConn) take() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	reads := c.reads
	c.reads = nil
	return reads
}

// recordingListener wraps accepted connections in recordingConn.
type recordingListener struct{ net.Listener }

func (l recordingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: c}, nil
}
