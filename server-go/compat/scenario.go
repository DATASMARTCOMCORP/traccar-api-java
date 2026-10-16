package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

// A Scenario is an ordered list of steps run in lockstep against both targets.
// Scenarios run in library order and build on each other (the first one creates the
// admin user that the others log in as).
type Scenario struct {
	Name  string
	Steps []Step
}

// A Step is either an API request (Method set) or device traffic (Device set).
type Step struct {
	Name string

	// API request.
	Actor     string // whose cookie jar to use; "" is a fresh anonymous client
	Method    string
	Path      string // may contain ${var}
	JSON      string // request body, sent as application/json
	Form      string // request body, sent as application/x-www-form-urlencoded
	Raw       []byte // request body sent as is, with RawType as Content-Type
	RawType   string
	Header    map[string]string // values may contain ${var}
	Save      map[string]string // var name → JSON path ("id", "0.id") or "$body" for the raw body
	Opaque    []string          // JSON paths compared by shape only (random by design), or "$body"
	Unordered []string          // JSON paths of arrays compared as multisets (Java HashSet order)
	Binary    bool              // non-JSON 2xx body (xlsx...): compare status and headers only
	CSV       bool              // CSV body: compare by column name (Java's column order varies)
	Token     bool              // 2xx body is a bearer token: alias its random id on both sides
	// Cross expands ${vars} with the other side's variables: A sends what B produced and
	// the reverse (e.g. each server must accept the other's tokens).
	Cross bool

	// Device traffic.
	Device *Traffic

	// Sleep waits on each side (for timeouts) and compares nothing.
	Sleep time.Duration
}

// Traffic sends raw device messages to a protocol port, like a tracker would.
type Traffic struct {
	Protocol  string   // key into target.ports
	Transport string   // "tcp", "udp" or "http" (osmand)
	Messages  [][]byte // tcp/udp: one write or datagram each; http: "path?query" (GET) or "POST path\nbody"
	// Separate sends each tcp/udp message on its own connection, so a decoder error that
	// closes one doesn't hide the rest.
	Separate bool
	// ReplyIdle overrides how long a connection must be quiet before its replies are
	// complete.
	ReplyIdle time.Duration
}

// side is the state of one target during a run: variables saved from its responses and
// one cookie jar per actor.
type side struct {
	t       *target
	db      *pgx.Conn
	vars    map[string]string
	clients map[string]*http.Client
	aliases map[string]string // random value → stable alias, see jsonOptions
	other   *side             // the opposite side, for Cross steps
	scope   map[string]string // variables used by expand for the current step
}

func newSide(t *target, db *pgx.Conn) *side {
	return &side{t: t, db: db, vars: map[string]string{}, clients: map[string]*http.Client{}, aliases: map[string]string{}}
}

func (r *side) client(actor string) *http.Client {
	if actor == "" {
		return &http.Client{Timeout: 60 * time.Second}
	}
	if c, ok := r.clients[actor]; ok {
		return c
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 60 * time.Second}
	r.clients[actor] = c
	return c
}

var varRef = regexp.MustCompile(`\$\{([A-Za-z0-9_]+|totp:[A-Z2-7]+)\}`)

// expand replaces ${name} with a variable, and ${totp:SECRET} with the current TOTP code
// for a base32 secret.
func (r *side) expand(s string) (string, error) {
	vars := r.scope
	if vars == nil {
		vars = r.vars
	}
	var missing []string
	out := varRef.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if secret, ok := strings.CutPrefix(name, "totp:"); ok {
			return totpCode(secret, time.Now())
		}
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined variables %v", missing)
	}
	return out, nil
}

// response is what a step produced on one target.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (r *side) do(ctx context.Context, s Step) (*response, error) {
	r.scope = r.vars
	if s.Cross {
		r.scope = r.other.vars
	}
	defer func() { r.scope = nil }()
	if s.Device != nil {
		return r.traffic(ctx, s.Device)
	}
	if s.Sleep > 0 {
		time.Sleep(s.Sleep)
		return &response{header: http.Header{}}, nil
	}
	path, err := r.expand(s.Path)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	contentType := ""
	switch {
	case s.JSON != "":
		js, err := r.expand(s.JSON)
		if err != nil {
			return nil, err
		}
		body, contentType = strings.NewReader(js), "application/json"
	case s.Form != "":
		form, err := r.expand(s.Form)
		if err != nil {
			return nil, err
		}
		body, contentType = strings.NewReader(form), "application/x-www-form-urlencoded"
	case s.Raw != nil:
		body, contentType = bytes.NewReader(s.Raw), s.RawType
	}
	req, err := http.NewRequestWithContext(ctx, s.Method, r.t.baseURL()+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range s.Header {
		if v, err = r.expand(v); err != nil {
			return nil, err
		}
		req.Header.Set(k, v)
	}
	resp, err := r.client(s.Actor).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := &response{status: resp.StatusCode, header: resp.Header, body: data}
	if err := r.save(s, out); err != nil {
		return out, err
	}
	if s.Token && out.status < 300 {
		id, err := tokenID(string(data))
		if err != nil {
			return out, err
		}
		r.aliases[id] = fmt.Sprintf("<token %d>", len(r.aliases)+1)
	}
	return out, nil
}

// tokenPayload is the signed part of a bearer token.
type tokenPayload struct {
	ID         json.Number `json:"i"`
	UserID     json.Number `json:"u"`
	Expiration string      `json:"e"`
}

// decodeToken reads a bearer token's payload: base64url of
// [signature length][DER signature][JSON {"i":id,"u":userId,"e":expiration}].
func decodeToken(token string) (tokenPayload, error) {
	var p tokenPayload
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 2 || int(raw[0])+1 >= len(raw) {
		return p, fmt.Errorf("malformed token %q", token)
	}
	dec := json.NewDecoder(bytes.NewReader(raw[1+int(raw[0]):]))
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil || p.ID == "" {
		return p, fmt.Errorf("token payload: %v", err)
	}
	return p, nil
}

func tokenID(token string) (string, error) {
	p, err := decodeToken(token)
	return string(p.ID), err
}

// diffTokens compares what both servers signed: the same user and expiration (within
// clockTolerance when the server chose it). Ids and signatures are random.
func diffTokens(a, b []byte) []string {
	pa, errA := decodeToken(string(a))
	pb, errB := decodeToken(string(b))
	if errA != nil || errB != nil {
		return []string{fmt.Sprintf("token: A=%v B=%v", errA, errB)}
	}
	var out []string
	if pa.UserID != pb.UserID {
		out = append(out, fmt.Sprintf("token user: A=%s B=%s", pa.UserID, pb.UserID))
	}
	if pa.Expiration != pb.Expiration && !datesClose(pa.Expiration, pb.Expiration) {
		out = append(out, fmt.Sprintf("token expiration: A=%s B=%s", pa.Expiration, pb.Expiration))
	}
	return out
}

func (r *side) save(s Step, resp *response) error {
	if len(s.Save) == 0 || resp.status >= 300 {
		return nil
	}
	var doc any
	for name, path := range s.Save {
		if path == "$body" {
			r.vars[name] = string(resp.body)
			continue
		}
		if doc == nil {
			var err error
			if doc, err = decodeJSON(resp.body); err != nil {
				return fmt.Errorf("save %s: %w", name, err)
			}
		}
		v, err := lookup(doc, path)
		if err != nil {
			return fmt.Errorf("save %s: %w", name, err)
		}
		r.vars[name] = v
	}
	return nil
}

func lookup(doc any, path string) (string, error) {
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return "", fmt.Errorf("no index %q in array of %d", part, len(v))
			}
			cur = v[i]
		default:
			return "", fmt.Errorf("can't descend into %T at %q", cur, part)
		}
	}
	switch v := cur.(type) {
	case string:
		return v, nil
	case nil:
		return "", fmt.Errorf("path %q not found", path)
	default:
		return strings.TrimSpace(render(v)), nil
	}
}

// traffic plays device messages. Replies are collected until the connection has been
// idle for replyIdle, so both targets' acknowledgements can be compared byte for byte.
func (r *side) traffic(ctx context.Context, tr *Traffic) (*response, error) {
	const replyIdle = 700 * time.Millisecond
	port, ok := r.t.ports[tr.Protocol]
	if !ok {
		return nil, fmt.Errorf("protocol %q not configured", tr.Protocol)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	out := &response{status: 0, header: http.Header{}}
	switch tr.Transport {
	case "http":
		client := &http.Client{Timeout: 30 * time.Second}
		for _, m := range tr.Messages {
			msg, err := r.expand(string(m))
			if err != nil {
				return nil, err
			}
			// "POST <path>\n<form body>" posts like the SIR clients, "JSON <path>\n<body>"
			// posts JSON, "RAW <request>" writes the bytes on a new connection; anything else
			// is a GET.
			var resp *http.Response
			if rest, ok := strings.CutPrefix(msg, "RAW "); ok {
				resp, err = rawRequest(addr, rest)
			} else if rest, ok := strings.CutPrefix(msg, "POST "); ok {
				path, body, _ := strings.Cut(rest, "\n")
				resp, err = client.Post("http://"+addr+path, "application/x-www-form-urlencoded", strings.NewReader(body))
			} else if rest, ok := strings.CutPrefix(msg, "JSON "); ok {
				path, body, _ := strings.Cut(rest, "\n")
				resp, err = client.Post("http://"+addr+path, "application/json", strings.NewReader(body))
			} else {
				resp, err = client.Get("http://" + addr + msg)
			}
			if err != nil {
				// The server closed the connection without answering (a decoder error).
				out.body = fmt.Appendf(out.body, "closed\n")
				continue
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			// One line per message, so each message's status is compared.
			out.body = fmt.Appendf(out.body, "%d %q\n", resp.StatusCode, data)
		}
	case "tcp", "udp":
		idle := replyIdle
		if tr.ReplyIdle > 0 {
			idle = tr.ReplyIdle
		}
		batches := [][][]byte{tr.Messages}
		if tr.Separate {
			batches = nil
			for _, m := range tr.Messages {
				batches = append(batches, [][]byte{m})
			}
		}
		for i, batch := range batches {
			if tr.Separate {
				out.body = fmt.Appendf(out.body, "#%d ", i) // which connection each reply came on
			}
			if err := exchange(ctx, tr.Transport, addr, batch, idle, out); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unknown transport %q", tr.Transport)
	}
	return out, nil
}

// exchange writes messages on one connection, reading the replies after each until the
// connection has been quiet for idle (or is closed).
func exchange(ctx context.Context, transport, addr string, messages [][]byte, idle time.Duration, out *response) error {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, transport, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	buf := make([]byte, 64*1024)
	for _, m := range messages {
		if _, err := conn.Write(m); err != nil {
			if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				out.body = fmt.Appendf(out.body, "closed\n")
				return nil
			}
			return err
		}
		for {
			_ = conn.SetReadDeadline(time.Now().Add(idle))
			n, err := conn.Read(buf)
			out.body = append(out.body, buf[:n]...)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break
				}
				if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
					out.body = fmt.Appendf(out.body, "closed\n")
					return nil
				}
				return err
			}
		}
	}
	return nil
}

// rawRequest sends request bytes as they are (a device's malformed or non-ASCII
// request line) on a new connection and reads one response.
func rawRequest(addr, request string) (*http.Response, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(request)); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, err
}

// headersCompared are the response headers clients depend on. Everything else (Date,
// Server, Content-Length...) may differ.
var headersCompared = []string{
	"Content-Type",
	"Content-Disposition",
	"Location",
	"WWW-Authenticate",
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
}

func compareResponses(s Step, a, b *response, aliasA, aliasB map[string]string) []string {
	var out []string
	if a.status != b.status {
		out = append(out, fmt.Sprintf("status: A=%d B=%d", a.status, b.status))
	}
	for _, h := range headersCompared {
		va, vb := a.header.Values(h), b.header.Values(h)
		if !slices.Equal(va, vb) {
			out = append(out, fmt.Sprintf("header %s: A=%q B=%q", h, va, vb))
		}
	}
	if ca, cb := cookies(a.header), cookies(b.header); ca != cb {
		out = append(out, fmt.Sprintf("Set-Cookie: A=%q B=%q", ca, cb))
	}

	// Error bodies may differ: Java sends stack traces, Go a short message (agreed in the plan).
	if a.status >= 400 || a.status != b.status {
		return out
	}
	switch {
	case s.Device != nil && s.Device.Transport == "http":
		if !bytes.Equal(a.body, b.body) {
			out = append(out, fmt.Sprintf("device replies: A=%q B=%q", a.body, b.body))
		}
	case s.Device != nil:
		if !bytes.Equal(a.body, b.body) {
			out = append(out, fmt.Sprintf("device reply: A=%s B=%s", hex.EncodeToString(a.body), hex.EncodeToString(b.body)))
		}
	case s.Token:
		out = append(out, diffTokens(a.body, b.body)...)
	case slices.Contains(s.Opaque, "$body"):
		if (len(a.body) == 0) != (len(b.body) == 0) {
			out = append(out, fmt.Sprintf("body shape: A=%d bytes B=%d bytes", len(a.body), len(b.body)))
		}
	case s.Binary:
	case s.CSV:
		out = append(out, diffCSV(a.body, b.body)...)
	case isJSON(a.header) && len(a.body) > 0 && !json.Valid(a.body) && !json.Valid(b.body):
		// Java labels some plain strings as JSON (tokens, file names): compare the bytes.
		if !bytes.Equal(a.body, b.body) {
			out = append(out, fmt.Sprintf("body: A=%q B=%q", clip(a.body), clip(b.body)))
		}
	case isJSON(a.header) && len(a.body) > 0:
		diffs, err := diffJSON(a.body, b.body, jsonOptions{
			opaque: s.Opaque, unordered: s.Unordered, aliasA: aliasA, aliasB: aliasB,
		})
		if err != nil {
			out = append(out, "body: "+err.Error())
		}
		for _, d := range diffs {
			out = append(out, "body "+d)
		}
	default:
		if !bytes.Equal(a.body, b.body) {
			out = append(out, fmt.Sprintf("body: A=%q B=%q", clip(a.body), clip(b.body)))
		}
	}
	return out
}

// cookies renders Set-Cookie headers with values replaced, keeping names and attributes.
func cookies(h http.Header) string {
	var parts []string
	for _, c := range (&http.Response{Header: h}).Cookies() {
		attrs := strings.Split(c.Raw, ";")
		attrs[0] = c.Name + "=<value>"
		for i := range attrs {
			attrs[i] = strings.TrimSpace(attrs[i])
		}
		parts = append(parts, strings.Join(attrs, "; "))
	}
	slices.Sort(parts)
	return strings.Join(parts, " | ")
}

func isJSON(h http.Header) bool {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt == "application/json"
}

func clip(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

// totpCode is the current RFC 6238 code (SHA-1, 6 digits, 30 s) for a base32 secret.
func totpCode(secret string, now time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "000000"
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}
