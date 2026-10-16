package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Socket is a step on a WebSocket to /api/socket (Step 8). Each side keeps its sockets
// by name across steps, so a socket opened before device traffic sees what it causes.
type Socket struct {
	Name   string            // the socket
	Open   bool              // connect, with the step's Actor cookies; the response is the handshake status
	Query  string            // on Open: the query string (may contain ${vars}), e.g. "token=${tok}"
	Header map[string]string // on Open: request headers (Origin)
	Send   string            // a text message to send
	// Read waits until the socket has been quiet for socketQuiet, then responds with a
	// JSON array of the messages received since the last read.
	Read  bool
	Close bool
}

// socketQuiet is how long a socket must receive nothing before a read completes.
const socketQuiet = 700 * time.Millisecond

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
	msgs [][]byte
	last time.Time
	done chan struct{}
}

func (c *wsClient) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if string(data) == "{}" {
			// A keepalive: both servers send one every 55 s from their own start, so it
			// lands in one side's read and not the other's. The hub's tests cover it.
			continue
		}
		c.mu.Lock()
		c.msgs = append(c.msgs, data)
		c.last = time.Now()
		c.mu.Unlock()
	}
}

// take waits for quiet, then returns the messages so far as a JSON array.
func (c *wsClient) take(ctx context.Context) []byte {
	start := time.Now()
	for {
		c.mu.Lock()
		last := c.last
		c.mu.Unlock()
		quietSince := last
		if start.After(last) {
			quietSince = start
		}
		if time.Since(quietSince) >= socketQuiet {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-c.done:
		case <-time.After(50 * time.Millisecond):
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []byte{'['}
	for i, m := range c.msgs {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, m...)
	}
	c.msgs = nil
	return append(out, ']')
}

func (r *side) socket(ctx context.Context, s Step) (*response, error) {
	op := s.Socket
	if r.sockets == nil {
		r.sockets = map[string]*wsClient{}
	}
	out := &response{status: http.StatusOK, header: http.Header{}}
	switch {
	case op.Open:
		query, err := r.expand(op.Query)
		if err != nil {
			return nil, err
		}
		url := "ws" + strings.TrimPrefix(r.t.baseURL(), "http") + "/api/socket"
		if query != "" {
			url += "?" + query
		}
		header := http.Header{}
		for k, v := range op.Header {
			if v, err = r.expand(v); err != nil {
				return nil, err
			}
			header.Set(k, v)
		}
		dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
		if s.Actor != "" {
			dialer.Jar = r.client(s.Actor).Jar
		}
		conn, resp, err := dialer.DialContext(ctx, url, header)
		if resp != nil {
			out.status = resp.StatusCode
		}
		if err != nil {
			if errors.Is(err, websocket.ErrBadHandshake) {
				return out, nil // compared by status
			}
			return out, err
		}
		c := &wsClient{conn: conn, done: make(chan struct{})}
		r.sockets[op.Name] = c
		go c.readLoop()
		return out, nil
	case op.Send != "":
		c := r.sockets[op.Name]
		if c == nil {
			return nil, fmt.Errorf("socket %q is not open", op.Name)
		}
		return out, c.conn.WriteMessage(websocket.TextMessage, []byte(op.Send))
	case op.Read:
		c := r.sockets[op.Name]
		if c == nil {
			return nil, fmt.Errorf("socket %q is not open", op.Name)
		}
		out.header.Set("Content-Type", "application/json")
		out.body = r.normalizePorts(c.take(ctx))
		return out, nil
	case op.Close:
		c := r.sockets[op.Name]
		if c == nil {
			return nil, fmt.Errorf("socket %q is not open", op.Name)
		}
		delete(r.sockets, op.Name)
		_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		<-c.done
		return out, nil
	}
	return nil, errors.New("empty socket step")
}

// normalizePorts replaces this side's ports (in the Host headers that text logs carry) with
// their protocol names, since each side listens on its own ports.
func (r *side) normalizePorts(b []byte) []byte {
	s := string(b)
	s = strings.ReplaceAll(s, ":"+strconv.Itoa(r.t.webPort), ":<web>")
	for name, port := range r.t.ports {
		s = strings.ReplaceAll(s, ":"+strconv.Itoa(port), ":<"+name+">")
	}
	return []byte(s)
}
