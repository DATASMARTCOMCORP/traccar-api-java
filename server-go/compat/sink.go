package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"
)

// sink receives what a server sends out (Step 9): HTTP posts (event forwarding, SMS) and
// e-mail over SMTP. Each target has its own, so a Sink step compares what each side sent.
// Configs refer to it as ${sinkHTTP} (a base URL) and ${sinkSMTP} (a port).
type sink struct {
	httpLn, smtpLn net.Listener
	srv            *http.Server

	mu    sync.Mutex
	items []json.RawMessage
	last  time.Time
}

func newSink() (*sink, error) {
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		httpLn.Close()
		return nil, err
	}
	s := &sink{httpLn: httpLn, smtpLn: smtpLn}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serveHTTP)}
	go s.srv.Serve(httpLn)
	go s.serveSMTP()
	return s, nil
}

func (s *sink) httpURL() string { return "http://" + s.httpLn.Addr().String() }
func (s *sink) smtpPort() string {
	_, port, _ := net.SplitHostPort(s.smtpLn.Addr().String())
	return port
}

func (s *sink) close() {
	s.srv.Close()
	s.smtpLn.Close()
}

func (s *sink) record(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	s.mu.Lock()
	s.items = append(s.items, data)
	s.last = time.Now()
	s.mu.Unlock()
}

// volatileHeaders differ between HTTP clients and are not compared.
var volatileHeaders = map[string]bool{
	"Accept": true, "Accept-Encoding": true, "Connection": true, "Content-Length": true,
	"Host": true, "User-Agent": true,
}

// body is a JSON body as JSON, anything else as text.
func body(contentType string, data []byte) any {
	if strings.HasPrefix(contentType, "application/json") && json.Valid(data) {
		return json.RawMessage(data)
	}
	return string(data)
}

// serveHTTP records a request. A path starting with /fail answers 500.
func (s *sink) serveHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	headers := map[string]string{}
	for k, v := range r.Header {
		if !volatileHeaders[k] {
			headers[k] = strings.Join(v, ", ")
		}
	}
	ct := r.Header.Get("Content-Type")
	b := body(ct, data)
	if raw, ok := b.(json.RawMessage); ok && r.URL.Path == "/forward" {
		b = withoutForwardAddress(raw)
	}
	s.record(map[string]any{
		"kind": "http", "method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery,
		"headers": headers, "body": b,
	})
	if strings.HasPrefix(r.URL.Path, "/fail") {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// withoutForwardAddress drops the forwarded position's address. Java serializes the
// payload on its HTTP client's thread, after the notifications of the event may have
// geocoded the position, so whether the address is there is a race in Java (MIGRATION.md
// Step 9). Positions get no address otherwise in these suites.
func withoutForwardAddress(raw json.RawMessage) any {
	var v map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	if p, ok := v["position"].(map[string]any); ok {
		delete(p, "address")
	}
	return v
}

// serveSMTP accepts any mail: EHLO with AUTH, MAIL, RCPT, DATA.
func (s *sink) serveSMTP() {
	for {
		c, err := s.smtpLn.Accept()
		if err != nil {
			return
		}
		go s.smtpSession(c)
	}
}

func (s *sink) smtpSession(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Minute))
	r := bufio.NewReader(c)
	reply := func(lines ...string) {
		for _, l := range lines {
			io.WriteString(c, l+"\r\n")
		}
	}
	reply("220 sink ESMTP")
	var from string
	var rcpt []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			reply("250-sink", "250-AUTH PLAIN LOGIN", "250 8BITMIME")
		case strings.HasPrefix(upper, "HELO"):
			reply("250 sink")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			reply("334 VXNlcm5hbWU6")
			r.ReadString('\n')
			reply("334 UGFzc3dvcmQ6")
			r.ReadString('\n')
			reply("235 ok")
		case strings.HasPrefix(upper, "AUTH"):
			reply("235 ok")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = strings.Trim(strings.TrimSpace(cmd[10:]), "<>")
			if i := strings.IndexByte(from, '>'); i >= 0 {
				from = from[:i]
			}
			rcpt = nil
			reply("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt = append(rcpt, strings.Trim(strings.TrimSpace(cmd[8:]), "<>"))
			reply("250 ok")
		case upper == "DATA":
			reply("354 go ahead")
			var msg bytes.Buffer
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				msg.WriteString(strings.TrimPrefix(l, "."))
			}
			s.recordMail(from, rcpt, msg.Bytes())
			reply("250 queued")
		case upper == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

var wordDecoder = &mime.WordDecoder{}

// recordMail records a message's envelope, decoded headers and decoded body.
func (s *sink) recordMail(from string, rcpt []string, raw []byte) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		s.record(map[string]any{"kind": "mail", "error": err.Error()})
		return
	}
	decode := func(h string) any {
		v, ok := m.Header[h]
		if !ok {
			return nil
		}
		d, err := wordDecoder.DecodeHeader(strings.Join(v, ", "))
		if err != nil {
			return strings.Join(v, ", ")
		}
		return d
	}
	address := func(h string) any {
		v := m.Header.Get(h)
		if v == "" {
			return nil
		}
		a, err := (&mail.AddressParser{WordDecoder: wordDecoder}).Parse(v)
		if err != nil {
			return v
		}
		return map[string]string{"name": a.Name, "address": a.Address}
	}
	data, _ := io.ReadAll(m.Body)
	switch strings.ToLower(m.Header.Get("Content-Transfer-Encoding")) {
	case "quoted-printable":
		data, _ = io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
	case "base64":
		data, _ = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(data)), ""))
	}
	ct := m.Header.Get("Content-Type")
	mt, params, _ := mime.ParseMediaType(ct)
	s.record(map[string]any{
		"kind": "mail", "envelopeFrom": from, "rcpt": rcpt,
		"from": address("From"), "to": decode("To"), "subject": decode("Subject"),
		"contentType": mt + "; charset=" + strings.ToLower(params["charset"]),
		"body":        strings.ReplaceAll(string(data), "\r\n", "\n"),
	})
}

// take waits until nothing arrived for socketQuiet, then returns what arrived since the
// last take, as a JSON array.
func (s *sink) take(ctx context.Context) []byte {
	start := time.Now()
	for {
		s.mu.Lock()
		last := s.last
		s.mu.Unlock()
		if last.Before(start) {
			last = start
		}
		if time.Since(last) >= socketQuiet {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []byte{'['}
	for i, it := range s.items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, it...)
	}
	s.items = nil
	return tokenParam.ReplaceAll(append(out, ']'), []byte("${1}<token>"))
}

// tokenParam is a token in a link of a notification: random on each side.
var tokenParam = regexp.MustCompile(`((?:token|passwordReset)=)[A-Za-z0-9_-]+`)

// expandSink replaces the sink placeholders in a config value.
func (s *sink) expand(v string) string {
	v = strings.ReplaceAll(v, "${sinkHTTP}", s.httpURL())
	return strings.ReplaceAll(v, "${sinkSMTP}", s.smtpPort())
}
