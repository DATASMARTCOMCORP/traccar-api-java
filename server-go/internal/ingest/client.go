package ingest

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// connectClient is TrackerClient.start: one connection now (a failure is returned, as
// Java logs "Connection failed"), and after it closes a new one every <protocol>.interval
// seconds, until a reconnection fails (Java's scheduled reconnection then stops too).
func (p *Processor) connectClient(l *Listener, proto Protocol, client Client, port int) error {
	cfg := p.cfg
	secure := cfg.Bool(proto.Name+".ssl", false)
	interval, err := cfg.Int64(proto.Name+".interval", 0)
	if err != nil {
		return err
	}
	address, _ := cfg.Lookup(proto.Name + ".address")
	timeout, err := p.protocolTimeout(proto.Name)
	if err != nil {
		return err
	}
	target := net.JoinHostPort(address, strconv.Itoa(port))
	dial := func() (net.Conn, error) {
		d := &net.Dialer{Timeout: 30 * time.Second}
		if !secure {
			return d.Dial("tcp", target)
		}
		// SSLContext.getDefault() in client mode: the chain is verified against the
		// default trust store, the host name isn't (no endpoint identification).
		return tls.DialWithDialer(d, "tcp", target, &tls.Config{
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				return verifyChain(raw)
			},
		})
	}
	c, err := dial()
	if err != nil {
		return fmt.Errorf("Connection failed: %w", err)
	}
	go func() {
		for {
			l.track(c)
			p.handleTCP(l, proto, client.NewPipeline(), c, timeout)
			if interval <= 0 {
				return
			}
			select {
			case <-time.After(time.Duration(interval) * time.Second):
			case <-p.ctx.Done():
				return
			}
			if c, err = dial(); err != nil {
				p.log.Warn("reconnection failed", "protocol", proto.Name, "error", err)
				return
			}
		}
	}()
	return nil
}

func verifyChain(raw [][]byte) error {
	if len(raw) == 0 {
		return errors.New("no certificate")
	}
	certs := make([]*x509.Certificate, len(raw))
	for i, r := range raw {
		c, err := x509.ParseCertificate(r)
		if err != nil {
			return err
		}
		certs[i] = c
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Intermediates: intermediates})
	return err
}

// HTTPResponseFramer is HttpResponseDecoder with HttpObjectAggregator(maxLength): each
// complete response (Content-Length or chunked) as an *HTTPResponse.
func HTTPResponseFramer(maxLength int) Framer {
	return FramerFunc(func(_ *Message, in *kit.Buf) any {
		data := in.Bytes()
		end := bytes.Index(data, []byte("\r\n\r\n"))
		if end < 0 {
			return nil
		}
		lines := strings.Split(string(data[:end]), "\r\n")
		status := 0
		if parts := strings.SplitN(lines[0], " ", 3); len(parts) >= 2 {
			status, _ = strconv.Atoi(parts[1])
		}
		length, chunked := -1, false
		for _, line := range lines[1:] {
			name, value, _ := strings.Cut(line, ":")
			value = strings.TrimSpace(value)
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "content-length":
				length, _ = strconv.Atoi(value)
			case "transfer-encoding":
				chunked = strings.EqualFold(value, "chunked")
			}
		}
		body := data[end+4:]
		var content []byte
		consumed := end + 4
		switch {
		case chunked:
			pos := 0
			for {
				eol := bytes.Index(body[pos:], []byte("\r\n"))
				if eol < 0 {
					return nil
				}
				sizeText, _, _ := strings.Cut(string(body[pos:pos+eol]), ";")
				size, err := strconv.ParseInt(strings.TrimSpace(sizeText), 16, 32)
				if err != nil {
					kit.Throw("invalid chunk size %q", sizeText)
				}
				pos += eol + 2
				if size == 0 {
					trailer := bytes.Index(body[pos:], []byte("\r\n"))
					if trailer < 0 {
						return nil
					}
					pos += trailer + 2
					break
				}
				if pos+int(size)+2 > len(body) {
					return nil
				}
				content = append(content, body[pos:pos+int(size)]...)
				pos += int(size) + 2
			}
			consumed += pos
		case length >= 0:
			if len(body) < length {
				return nil
			}
			content = body[:length]
			consumed += length
		}
		if len(content) > maxLength {
			kit.Throw("TooLongHttpContentException: Response entity too large")
		}
		in.Skip(consumed)
		return &HTTPResponse{Status: status, Body: append([]byte(nil), content...)}
	})
}
