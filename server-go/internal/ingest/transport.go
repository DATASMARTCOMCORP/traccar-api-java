package ingest

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// A Decoder is a protocol decoder (BaseProtocolDecoder.decode): it turns one message (a
// *kit.Buf, or a string after a text pipeline's StringDecoder) into nil, a position or a
// slice of positions. A nil slice is Java's null and a non-nil empty slice an empty
// collection: only null makes an empty-message position (database.saveEmpty). A *kit.Fault (a panic) or an error is where the Java decoder
// throws: the connection is closed.
type Decoder interface {
	Decode(m *Message, msg any) any
}

// DecoderFunc adapts a function to Decoder.
type DecoderFunc func(m *Message, msg any) any

func (f DecoderFunc) Decode(m *Message, msg any) any { return f(m, msg) }

// A CommandEncoder is a protocol encoder (BaseProtocolEncoder.encodeCommand): the bytes
// (*kit.Buf) or text to send for a command, or nil when it can't be encoded.
type CommandEncoder interface {
	Encode(m *Message, command *model.Command) any
}

// Pipeline is a protocol's handlers for one TCP connection or one UDP socket.
type Pipeline struct {
	Framer  Framer // TCP: splits the stream; nil passes each read as it comes
	Text    bool   // StringDecoder: the decoder gets strings (UTF-8)
	TextOut bool   // StringEncoder: string replies are sent (as UTF-8)
	Decoder Decoder
	Encoder CommandEncoder
}

// Server is one listener of a protocol (a TrackerServer): TCP, UDP (Datagram) or HTTP.
type Server struct {
	Datagram    bool
	NewPipeline func() Pipeline // TCP and UDP
	HTTP        HTTPDecoder     // an HTTP server (BaseHttpProtocolDecoder)
}

// Protocol is a device protocol: its listeners and the data commands its encoder takes
// (custom commands are always accepted).
type Protocol struct {
	Name         string
	Servers      []Server
	DataCommands []string
	// OwnQueue: the decoder reads the command queue itself, so onMessageEvent doesn't
	// send queued commands (OsmAndProtocolDecoder overrides sendQueuedCommands).
	OwnQueue bool
}

// decodeSafely runs a decoder, turning a Fault panic into an error.
func decodeSafely(d Decoder, m *Message, msg any) (result []*model.Position, err error) {
	defer kit.Recover(&err)
	switch v := d.Decode(m, msg).(type) {
	case nil:
	case *model.Position:
		if v != nil {
			result = []*model.Position{v}
		}
	case []*model.Position:
		result = v
	default:
		return nil, fmt.Errorf("decoder returned %T", v)
	}
	return result, nil
}

func frameSafely(f Framer, m *Message, in *kit.Buf) (frame any, err error) {
	defer kit.Recover(&err)
	return f.Frame(m, in), nil
}

// toText is Netty's StringDecoder (UTF-8) for a text pipeline.
func toText(msg any) any {
	if b, ok := msg.(*kit.Buf); ok {
		return kit.UTF8(b.Bytes())
	}
	return msg
}

// encodeReply turns a reply into bytes: a string only passes a pipeline with a
// StringEncoder (Netty fails the write otherwise).
func encodeReply(pl Pipeline, data any) ([]byte, error) {
	switch v := data.(type) {
	case *kit.Buf:
		return append([]byte(nil), v.Bytes()...), nil
	case []byte:
		return v, nil
	case string:
		if pl.TextOut {
			return []byte(v), nil // StringEncoder's charset is UTF-8
		}
	case nil:
		return nil, errors.New("NullPointerException: msg")
	}
	return nil, fmt.Errorf("unsupported message type: %T", data)
}

// ServeProtocol opens a protocol's servers on <name>.address and the port. As in Java's
// ServerManager, a server that can't listen doesn't stop the others: the listener has the
// servers that started, and the error says which didn't.
func (p *Processor) ServeProtocol(proto Protocol, port int) (*Listener, error) {
	l := &Listener{p: p, protocol: proto.Name}
	var errs []error
	for _, s := range proto.Servers {
		var err error
		switch {
		case s.HTTP != nil:
			err = p.serveHTTP(l, proto, port, s.HTTP)
		case s.Datagram:
			err = p.serveUDP(l, proto, s, port)
		default:
			err = p.serveTCP(l, proto, s, port)
		}
		errs = append(errs, err)
	}
	return l, errors.Join(errs...)
}

func (p *Processor) serveTCP(l *Listener, proto Protocol, s Server, port int) error {
	timeout, err := p.protocolTimeout(proto.Name)
	if err != nil {
		return err
	}
	for _, addr := range listenAddresses(p.cfg, proto.Name, port) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("%s: %w", proto.Name, err)
		}
		l.listeners = append(l.listeners, ln)
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						p.log.Warn("accept failed", "protocol", proto.Name, "error", err)
					}
					return
				}
				l.track(c)
				go p.handleTCP(l, proto, s.NewPipeline(), c, timeout)
			}
		}()
	}
	return nil
}

// handleTCP is one TCP connection: frames go to the decoder in order, each processed
// before the next is read (as on Netty's event loop without buffering).
func (p *Processor) handleTCP(l *Listener, proto Protocol, pl Pipeline, c net.Conn, timeout time.Duration) {
	defer l.untrack(c)
	defer c.Close()
	remote := c.RemoteAddr().String()
	var mu sync.Mutex
	conn := &Conn{p: p, protocol: proto.Name, local: c.LocalAddr().String(), dataCommands: proto.DataCommands,
		ownQueue: proto.OwnQueue}
	conn.write = func(_ string, data any) error {
		b, err := encodeReply(pl, data)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
		_, err = c.Write(b)
		return err
	}
	conn.pipeline = &pl
	defer p.channelInactive(conn, remote)

	in := kit.Empty()
	chunk := make([]byte, 64*1024)
	for {
		if timeout > 0 {
			_ = c.SetReadDeadline(time.Now().Add(timeout))
		}
		n, err := c.Read(chunk)
		if n > 0 {
			in.WriteBytes(chunk[:n])
			if !p.consume(conn, pl, remote, in) {
				return
			}
			// Drop consumed bytes (frames were copied out).
			rest := append([]byte(nil), in.Bytes()...)
			in = kit.NewBuf(rest)
			in.SetIndex(0, len(rest))
		}
		if err != nil {
			return // closed, reset or idle (IdleStateHandler)
		}
	}
}

// consume runs the framer over the cumulated bytes and handles each message; false
// closes the connection (a decoder or framer exception).
func (p *Processor) consume(conn *Conn, pl Pipeline, remote string, in *kit.Buf) bool {
	for in.IsReadable() {
		if pl.Framer == nil {
			msg := in.ReadSlice(in.ReadableBytes()).Copy()
			return p.handleMessage(conn, pl, remote, msg)
		}
		before := in.ReadableBytes()
		m := NewMessage(p.ctx, conn, remote, time.Now())
		frame, err := frameSafely(pl.Framer, m, in)
		if err != nil {
			p.log.Warn("["+conn.protocol+" "+remote+"] error", "error", err)
			return false
		}
		if frame == nil {
			if in.ReadableBytes() == before {
				return true
			}
			continue
		}
		if b, ok := frame.(*kit.Buf); ok {
			frame = b.Copy()
		}
		if !p.handleMessage(conn, pl, remote, frame) {
			return false
		}
	}
	return true
}

// handleMessage is ExtendedObjectDecoder.channelRead for one message.
func (p *Processor) handleMessage(conn *Conn, pl Pipeline, remote string, msg any) bool {
	if pl.Text {
		msg = toText(msg)
	}
	// saveOriginal hex-dumps the whole message (from index 0, whatever the decoder read),
	// strings as getBytes(US_ASCII).
	var raw []byte
	switch v := msg.(type) {
	case *kit.Buf:
		raw = append([]byte(nil), v.Array()[:v.WriterIndex()]...)
	case string:
		raw = kit.ASCIIBytes(v)
	}
	conn.loop.Lock()
	m := NewMessage(p.ctx, conn, remote, time.Now())
	positions, err := decodeSafely(pl.Decoder, m, msg)
	var wait func()
	if err == nil {
		wait, err = m.finish(p.ctx, positions, raw)
	}
	conn.loop.Unlock()
	if err != nil {
		p.log.Warn("["+conn.protocol+" "+remote+"] error", "error", err)
		return conn.datagram // UDP channels stay open
	}
	if p.delayAck {
		wait()
	}
	return true
}

func (p *Processor) serveUDP(l *Listener, proto Protocol, s Server, port int) error {
	for _, addr := range listenAddresses(p.cfg, proto.Name, port) {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("%s: %w", proto.Name, err)
		}
		l.packets = append(l.packets, pc)
		pl := s.NewPipeline()
		conn := &Conn{p: p, protocol: proto.Name, datagram: true, local: pc.LocalAddr().String(), pipeline: &pl,
			dataCommands: proto.DataCommands, ownQueue: proto.OwnQueue}
		conn.write = func(remote string, data any) error {
			b, err := encodeReply(pl, data)
			if err != nil {
				return err
			}
			to, err := net.ResolveUDPAddr("udp", remote)
			if err != nil {
				return err
			}
			_, err = pc.WriteTo(b, to)
			return err
		}
		go func() {
			buf := make([]byte, 65536)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				msg := kit.NewBuf(bytes.Clone(buf[:n]))
				p.handleMessage(conn, pl, from.String(), msg)
			}
		}()
	}
	return nil
}

// track and untrack keep the open TCP connections, to close them at shutdown.
func (l *Listener) track(c net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conns == nil {
		l.conns = map[net.Conn]struct{}{}
	}
	l.conns[c] = struct{}{}
}

func (l *Listener) untrack(c net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.conns, c)
}

// CommandEncoderFunc adapts a function to CommandEncoder.
type CommandEncoderFunc func(m *Message, command *model.Command) any

func (f CommandEncoderFunc) Encode(m *Message, c *model.Command) any { return f(m, c) }
