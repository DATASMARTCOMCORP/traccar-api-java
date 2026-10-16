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
)

// Conn is one device connection, Netty's Channel: a TCP or HTTP connection, or a
// protocol's UDP socket.
type Conn struct {
	p        *Processor
	protocol string
	datagram bool
	http     bool   // HTTP protocols: no live commands, no offline status on disconnect
	local    string // the local address
	// write sends a reply or a command and waits for it to be written (nil for HTTP,
	// where the decoder returns its response).
	write    func(remote string, data any) error
	pipeline *Pipeline // TCP and UDP
	// dataCommands are the protocol's supported data commands (setSupportedDataCommands).
	dataCommands []string
	ownQueue     bool // Protocol.OwnQueue

	// loop serializes the connection's work as Netty's event loop does: decoding and
	// commands sent from elsewhere (the API) never run at the same time.
	loop sync.Mutex
}

// Conn implements Environment.

func (c *Conn) Protocol() string       { return c.protocol }
func (c *Conn) Config() *config.Config { return c.p.cfg }

func (c *Conn) DeviceSession(ctx context.Context, remote string, uniqueIDs []string) (*DeviceSession, error) {
	return c.p.sessions.deviceSession(ctx, endpoint{conn: c, remote: remote}, uniqueIDs...)
}

// QueuedCommands is CommandsManager.readQueuedCommands: up to limit (0: all) of the
// device's queued commands, oldest first, deleted as they are read, each with a
// queuedCommandSent event.
func (c *Conn) QueuedCommands(ctx context.Context, deviceID int64, limit int) ([]model.QueuedCommand, error) {
	p := c.p
	commands, err := p.st.QueuedCommands(ctx, deviceID, limit)
	if err != nil {
		return nil, err
	}
	var events []*model.Event
	for _, q := range commands {
		if err := store.Delete[model.QueuedCommand](ctx, p.st, q.ID); err != nil {
			return nil, err
		}
		e := newDeviceEvent(model.EventQueuedCommandSent, q.DeviceID)
		e.Attributes.Set("id", q.ID)
		events = append(events, e)
	}
	// Java collects the events in a HashMap keyed by identity-hashed events, so with
	// several commands their order changes between runs; Go keeps queue order.
	p.storeEvents(ctx, events, nil)
	return commands, nil
}

// UpdateNotificationToken is CommandsManager.updateNotificationToken.
func (c *Conn) UpdateNotificationToken(ctx context.Context, deviceID int64, token string) error {
	p := c.p
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

func (c *Conn) Device(ctx context.Context, deviceID int64) (*model.Device, error) {
	d, err := store.Get[model.Device](ctx, c.p.st, deviceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *Conn) Lookup(ctx context.Context, deviceID int64, name string, server, cfg bool) (any, error) {
	dc, err := c.p.loadContext(ctx, deviceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errJava // Java's lookup dereferences the missing device
	}
	if err != nil {
		return nil, err
	}
	return dc.raw(ctx, key{name: name, server: server, config: cfg})
}

func (c *Conn) Send(remote string, data any) {
	if c.write != nil {
		if err := c.write(remote, data); err != nil {
			c.p.log.Warn("["+c.protocol+" "+remote+"] reply not sent", "error", err)
		}
	}
}

func (c *Conn) LocalAddress() string { return c.local }

// finish is what ExtendedObjectDecoder.channelRead does after decode (positions nil is the
// decoder's null, a non-nil empty slice its empty collection):
// BaseProtocolDecoder.onMessageEvent (statistics, online status, queued commands), the
// empty-message case, and handing the positions to processing. raw is the message, for
// database.saveOriginal. The returned function waits until the positions are processed
// (server.delayAcknowledgement).
func (m *Message) finish(ctx context.Context, positions []*model.Position, raw []byte) (wait func(), err error) {
	c := m.env.(*Conn)
	p := c.p
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
		if ds, err := m.DeviceSession(); err == nil && ds != nil {
			ids = append(ids, ds.DeviceID)
		}
	} else {
		ids = model.NewLongSetOf(ids).Items()
	}
	for _, id := range ids {
		now := time.Now()
		p.sessions.updateDevice(ctx, id, model.StatusOnline, &now)
		// sendQueuedCommands: a failure aborts the message, as Java's exception does.
		if !c.ownQueue {
			if err := c.sendQueuedCommands(m, id); err != nil {
				return nil, err
			}
		}
	}

	// handleEmptyMessage: only for null, not for an empty list of positions.
	if positions == nil && p.saveEmpty {
		if ds, err := m.DeviceSession(); err == nil && ds != nil {
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
		p.submit(c, pos, wg.Done)
	}
	return wg.Wait, nil
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

// serveHTTP listens for an HTTP protocol on <protocol>.address (a comma-separated list,
// or all interfaces) and the given port, until the listener is closed.
func (p *Processor) serveHTTP(l *Listener, proto Protocol, port int, dec HTTPDecoder) error {
	protocol := proto.Name
	timeout, err := p.protocolTimeout(protocol)
	if err != nil {
		return err
	}
	conns := sync.Map{} // net.Conn → *Conn
	srv := &http.Server{
		Handler:     p.httpHandler(protocol, dec),
		IdleTimeout: timeout,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			conn := &Conn{p: p, protocol: protocol, http: true, local: c.LocalAddr().String(),
				dataCommands: proto.DataCommands, ownQueue: proto.OwnQueue}
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
	var lns []net.Listener
	for _, addr := range listenAddresses(p.cfg, protocol, port) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, ln := range lns {
				ln.Close()
			}
			return fmt.Errorf("%s: %w", protocol, err)
		}
		lns = append(lns, ln)
	}
	l.https = append(l.https, srv)
	for _, ln := range lns {
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				p.log.Warn("protocol server stopped", "protocol", protocol, "error", err)
			}
		}()
	}
	return nil
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
		ctx := p.ctx
		m := NewMessage(ctx, conn, r.RemoteAddr, time.Now())
		resp, positions, err := dec.DecodeHTTP(ctx, m, r, body)
		if err != nil {
			// MainEventHandler.exceptionCaught: logged, and the connection is closed.
			p.log.Warn("["+conn.protocol+" "+r.RemoteAddr+"] error", "error", err)
			panic(http.ErrAbortHandler)
		}
		// database.saveOriginal applies to byte and string messages, not HTTP requests.
		// The Java decoder has sent its response when onMessageEvent fails (a queued
		// command it can't send); the connection is then closed.
		if p.delayAck {
			wait, err := m.finish(ctx, positions, nil)
			if err != nil {
				p.log.Warn("["+conn.protocol+" "+r.RemoteAddr+"] error", "error", err)
				writeNettyResponse(w, resp)
				panic(http.ErrAbortHandler)
			}
			wait()
			writeNettyResponse(w, resp)
			return
		}
		writeNettyResponse(w, resp)
		if _, err := m.finish(ctx, positions, nil); err != nil {
			p.log.Warn("["+conn.protocol+" "+r.RemoteAddr+"] error", "error", err)
			panic(http.ErrAbortHandler)
		}
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
	https     []*http.Server
	listeners []net.Listener
	packets   []net.PacketConn

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Close stops accepting connections and closes open ones.
func (l *Listener) Close() error {
	var errs []error
	for _, srv := range l.https {
		errs = append(errs, srv.Close())
	}
	for _, ln := range l.listeners {
		errs = append(errs, ln.Close())
	}
	for _, pc := range l.packets {
		errs = append(errs, pc.Close())
	}
	l.mu.Lock()
	for c := range l.conns {
		c.Close()
	}
	l.mu.Unlock()
	return errors.Join(errs...)
}
