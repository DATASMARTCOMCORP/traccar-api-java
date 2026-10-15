package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Conn is one device connection, Netty's Channel: a TCP or HTTP connection, or a
// protocol's UDP socket.
type Conn struct {
	p        *Processor
	protocol string
	datagram bool
	http     bool // HTTP protocols: no live commands, no offline status on disconnect
}

// Message is the context of one received message: what BaseProtocolDecoder gives Java
// decoders.
type Message struct {
	conn   *Conn
	remote string // host:port of the sender
	Now    time.Time
}

// Protocol is the protocol name.
func (m *Message) Protocol() string { return m.conn.protocol }

// Config is the server configuration.
func (m *Message) Config() *config.Config { return m.conn.p.cfg }

// NewPosition is new Position(protocol), stamped with the server time.
func (m *Message) NewPosition() *model.Position {
	protocol := m.conn.protocol
	return &model.Position{Protocol: &protocol, ServerTime: model.NewTime(time.Now())}
}

func (m *Message) endpoint() endpoint {
	return endpoint{conn: m.conn, remote: m.remote}
}

// DeviceSession is BaseProtocolDecoder.getDeviceSession. Nil means unknown; an error means
// the Java decoder would throw (a disabled device, a database failure).
func (m *Message) DeviceSession(ctx context.Context, uniqueIDs ...string) (*DeviceSession, error) {
	return m.conn.p.sessions.deviceSession(ctx, m.endpoint(), uniqueIDs...)
}

// LastLocation is BaseProtocolDecoder.getLastLocation: a position without a fix of its
// own, completed from the last position during processing.
func (m *Message) LastLocation(pos *model.Position, deviceTime *time.Time) {
	if pos.DeviceID != 0 {
		pos.Outdated = true
		if deviceTime != nil {
			pos.DeviceTime = model.NewTime(*deviceTime)
		}
	}
}

// ConvertSpeed is BaseProtocolDecoder.convertSpeed: <protocol>.speed overrides the units
// the protocol reports (kn by default), and the result is in knots.
func (m *Message) ConvertSpeed(value float64, defaultUnits string) float64 {
	switch m.conn.p.cfg.String(m.conn.protocol+".speed", defaultUnits) {
	case "kmh":
		return units.KnotsFromKph(value)
	case "mps":
		return units.KnotsFromMps(value)
	case "mph":
		return units.KnotsFromMph(value)
	}
	return value
}

// QueuedCommands is CommandsManager.readQueuedCommands: up to limit (0: all) of the
// device's queued commands, oldest first, deleted as they are read, each with a
// queuedCommandSent event.
func (m *Message) QueuedCommands(ctx context.Context, deviceID int64, limit int) ([]model.QueuedCommand, error) {
	p := m.conn.p
	commands, err := p.st.QueuedCommands(ctx, deviceID, limit)
	if err != nil {
		return nil, err
	}
	var events []*model.Event
	for _, c := range commands {
		if err := store.Delete[model.QueuedCommand](ctx, p.st, c.ID); err != nil {
			return nil, err
		}
		e := newDeviceEvent(model.EventQueuedCommandSent, c.DeviceID)
		e.Attributes.Set("id", c.ID)
		events = append(events, e)
	}
	// Java collects the events in a HashMap keyed by identity-hashed events, so with
	// several commands their order changes between runs; Go keeps queue order.
	p.storeEvents(ctx, events, nil)
	return commands, nil
}

// UpdateNotificationToken is CommandsManager.updateNotificationToken.
func (m *Message) UpdateNotificationToken(ctx context.Context, deviceID int64, token string) error {
	p := m.conn.p
	ref := new(byte)
	if err := p.cache.addDevice(ctx, deviceID, ref); err != nil {
		return err
	}
	defer p.cache.removeDevice(deviceID, ref)
	dc, err := p.loadContext(ctx, deviceID)
	if err != nil {
		return err
	}
	setString(&dc.device.Attributes, "notificationTokens", token)
	return p.st.UpdateDeviceAttributes(ctx, &dc.device)
}

// finish is what ExtendedObjectDecoder.channelRead does after decode:
// BaseProtocolDecoder.onMessageEvent (statistics, online status, queued commands), the
// empty-message case, and handing the positions to processing. raw is the message, for
// database.saveOriginal. The returned function waits until the positions are processed
// (server.delayAcknowledgement).
func (m *Message) finish(ctx context.Context, positions []*model.Position, raw []byte, text bool) (wait func()) {
	p := m.conn.p
	p.stats.messageReceived(ctx)

	// onMessageEvent: the devices of the positions, or the endpoint's device.
	var ids []int64
	seen := model.NewLongSet()
	for _, pos := range positions {
		if seen.Add(pos.DeviceID) {
			ids = append(ids, pos.DeviceID)
		}
	}
	if len(ids) == 0 {
		if ds, err := m.DeviceSession(ctx); err == nil && ds != nil {
			ids = append(ids, ds.DeviceID)
		}
	} else {
		ids = model.NewLongSetOf(ids).Items()
	}
	for _, id := range ids {
		now := time.Now()
		p.sessions.updateDevice(ctx, id, model.StatusOnline, &now)
	}

	if len(positions) == 0 && p.saveEmpty {
		if ds, err := m.DeviceSession(ctx); err == nil && ds != nil {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			m.LastLocation(pos, nil)
			positions = append(positions, pos)
		}
	}

	var wg sync.WaitGroup
	for _, pos := range positions {
		if p.saveOriginal && raw != nil {
			pos.Attributes.Set(model.KeyOriginal, hexDump(raw))
		}
		if p.remoteAddress {
			pos.Attributes.Set(model.KeyIP, hostString(m.remote))
		}
		wg.Add(1)
		p.submit(m.conn, pos, wg.Done)
	}
	return wg.Wait
}

func hexDump(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 2*len(b))
	for i, c := range b {
		out[2*i], out[2*i+1] = digits[c>>4], digits[c&15]
	}
	return string(out)
}

// HTTPResponse is the reply of an HTTP protocol (BaseHttpProtocolDecoder.sendResponse):
// a status and a body, sent with only a content-length header, as Netty does.
type HTTPResponse struct {
	Status int
	Body   []byte
}

// HTTPDecoder decodes one request of an HTTP protocol. It returns the response, the
// decoded positions, or an error where the Java decoder throws, which closes the
// connection without a response.
type HTTPDecoder interface {
	DecodeHTTP(ctx context.Context, m *Message, r *http.Request, body []byte) (*HTTPResponse, []*model.Position, error)
}

// maxHTTPLength is BaseProtocol.MAX_HTTP_LENGTH (HttpObjectAggregator).
const maxHTTPLength = 64 * 1024

type connKey struct{}

// ServeHTTP listens for an HTTP protocol on <protocol>.address (a comma-separated list,
// or all interfaces) and the given port, until Shutdown.
func (p *Processor) ServeHTTP(protocol string, port int, dec HTTPDecoder) (*Listener, error) {
	timeout, err := p.protocolTimeout(protocol)
	if err != nil {
		return nil, err
	}
	l := &Listener{p: p, protocol: protocol}
	conns := sync.Map{} // net.Conn → *Conn
	srv := &http.Server{
		Handler:     p.httpHandler(protocol, dec),
		IdleTimeout: timeout,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			conn := &Conn{p: p, protocol: protocol, http: true}
			conns.Store(c, conn)
			return context.WithValue(ctx, connKey{}, conn)
		},
		ConnState: func(c net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				if v, ok := conns.LoadAndDelete(c); ok {
					p.channelInactive(v.(*Conn), c.RemoteAddr().String())
				}
			}
		},
		ErrorLog: nil,
	}
	l.http = srv
	for _, addr := range listenAddresses(p.cfg, protocol, port) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("%s: %w", protocol, err)
		}
		l.listeners = append(l.listeners, ln)
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				p.log.Warn("protocol server stopped", "protocol", protocol, "error", err)
			}
		}()
	}
	return l, nil
}

// protocolTimeout is the idle timeout: <protocol>.timeout, else server.timeout (1800 s).
func (p *Processor) protocolTimeout(protocol string) (time.Duration, error) {
	t, err := p.cfg.Int(protocol+".timeout", 0)
	if err != nil {
		return 0, err
	}
	if t == 0 {
		if t, err = p.cfg.Int("server.timeout", 1800); err != nil {
			return 0, err
		}
	}
	return time.Duration(t) * time.Second, nil
}

func listenAddresses(cfg *config.Config, protocol string, port int) []string {
	v, ok := cfg.Lookup(protocol + ".address")
	if !ok {
		return []string{":" + strconv.Itoa(port)}
	}
	var out []string
	for _, a := range strings.Split(v, ",") {
		out = append(out, net.JoinHostPort(strings.TrimSpace(a), strconv.Itoa(port)))
	}
	return out
}

func (p *Processor) httpHandler(protocol string, dec HTTPDecoder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := r.Context().Value(connKey{}).(*Conn)
		body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPLength+1))
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		if len(body) > maxHTTPLength {
			writeNettyResponse(w, &HTTPResponse{Status: http.StatusRequestEntityTooLarge})
			return
		}
		m := &Message{conn: conn, remote: r.RemoteAddr, Now: time.Now()}
		ctx := p.ctx
		resp, positions, err := dec.DecodeHTTP(ctx, m, r, body)
		if err != nil {
			// MainEventHandler.exceptionCaught: logged, and the connection is closed.
			p.log.Warn("["+conn.protocol+" "+r.RemoteAddr+"] error", "error", err)
			panic(http.ErrAbortHandler)
		}
		raw := []byte(r.Method + " " + r.RequestURI)
		if p.delayAck {
			wait := m.finish(ctx, positions, raw, true)
			wait()
			writeNettyResponse(w, resp)
			return
		}
		writeNettyResponse(w, resp)
		m.finish(ctx, positions, raw, true)
	})
}

// writeNettyResponse writes a response like Netty's DefaultFullHttpResponse with a
// content-length header and nothing else.
func writeNettyResponse(w http.ResponseWriter, resp *HTTPResponse) {
	if resp == nil {
		return
	}
	h := w.Header()
	h["Date"] = nil
	h["Content-Type"] = nil
	h.Set("Content-Length", strconv.Itoa(len(resp.Body)))
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// channelInactive is MainEventHandler.channelInactive for a closed connection.
func (p *Processor) channelInactive(c *Conn, remote string) {
	supportsOffline := !c.http && (p.ignoreOffline == nil || !p.ignoreOffline.Contains(c.protocol))
	p.sessions.disconnected(p.ctx, endpoint{conn: c, remote: remote}, supportsOffline)
}

// Listener is a protocol's open server sockets.
type Listener struct {
	p         *Processor
	protocol  string
	http      *http.Server
	listeners []net.Listener
}

// Close stops accepting connections and closes open ones.
func (l *Listener) Close() error {
	if l.http != nil {
		return l.http.Close()
	}
	var errs []error
	for _, ln := range l.listeners {
		errs = append(errs, ln.Close())
	}
	return errors.Join(errs...)
}
