package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/live"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// upgrader accepts any origin: Jetty's WebSocket servlet doesn't check it (the session's
// origin is checked instead).
var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// socketUser is AsyncSocketServlet's creator: the user of the "token" parameter (a
// failing login fails the upgrade), else the user id stored in the session when its
// origin is valid. Java reads the session attribute as is: no expiration or disabled
// check. 0 means no socket.
func (a *API) socketUser(r *http.Request) (int64, error) {
	if tokens := r.URL.Query()["token"]; len(tokens) > 0 {
		user, _, err := a.loginToken(r.Context(), tokens[0])
		if err != nil {
			return 0, err
		}
		if user == nil {
			return 0, errLoginRejected
		}
		return user.ID, nil
	}
	sess := a.sessions.get(r)
	if sess == nil || !originValid(sess, r) {
		return 0, nil
	}
	return sess.userID, nil
}

// latestPositions is PositionUtil.getLatestPositions: the latest positions of the devices
// the user sees.
func (a *API) latestPositions(r *http.Request, userID int64) ([]model.Position, error) {
	visible, err := store.IDs[model.Device](r.Context(), a.st, store.Permission(store.KindUser, userID, store.KindDevice))
	if err != nil {
		return nil, err
	}
	positions, err := a.st.LatestPositions(r.Context(), 0, a.opts.PositionPeriod)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(positions, func(p model.Position) bool { return !slices.Contains(visible, p.DeviceID) }), nil
}

// serveSocket is /api/socket (AsyncSocketServlet and AsyncSocket): on open, the latest
// positions; then devices, positions, events and (on request) logs as they happen, and
// {} every 55 seconds. The connection closes after web.timeout without traffic.
func (a *API) serveSocket(w http.ResponseWriter, r *http.Request) {
	if a.opts.Live == nil {
		a.cors(w, r)
		a.fail(w, r, errNoRoute)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		// Not an upgrade: HttpServlet's default doGet (and others) of the WebSocket servlet.
		w.Header().Set("Content-Type", "text/html;charset=iso-8859-1")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	userID, err := a.socketUser(r)
	if err != nil {
		// The creator throws: Jetty answers 500.
		w.Header().Set("Content-Type", "text/html;charset=iso-8859-1")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if userID == 0 {
		// The creator returns null: Jetty ends the request without upgrading, with 200.
		w.WriteHeader(http.StatusOK)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // the upgrader replied
	}
	defer conn.Close()

	idle := newIdleTimer(a.opts.SocketTimeout, func() { conn.Close() })
	defer idle.stop()

	positions, err := a.latestPositions(r, userID)
	if err != nil {
		return // Java's onWebSocketOpen throws: the socket closes
	}
	first := []byte(`{"positions":[`)
	for i, p := range positions {
		if i > 0 {
			first = append(first, ',')
		}
		data, err := p.MarshalJSON()
		if err != nil {
			return
		}
		first = append(first, data...)
	}
	first = append(first, ']', '}')

	l := live.NewListener()
	l.Send(first)
	if err := a.opts.Live.AddListener(r.Context(), userID, l); err != nil {
		return
	}
	defer a.opts.Live.RemoveListener(userID, l)

	// The writer: one goroutine, so messages keep their order.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range l.Messages() {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				conn.Close()
				continue // drain until the listener is removed
			}
			idle.reset()
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		idle.reset()
		var doc any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if dec.Decode(&doc) != nil {
			slog.Warn("Socket JSON parsing error")
			continue
		}
		// json.hasNonNull("logs") on an object.
		if msg, ok := doc.(map[string]any); ok {
			if v, ok := msg["logs"]; ok && v != nil {
				l.SetLogs(asBoolean(v))
			}
		}
	}
	a.opts.Live.RemoveListener(userID, l)
	wg.Wait()
}

// asBoolean is Jackson's JsonNode.asBoolean(): booleans as they are, integers when not
// zero, the text "true" (trimmed); false for anything else.
func asBoolean(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i != 0
		}
		return false
	case string:
		return strings.TrimSpace(x) == "true"
	}
	return false
}

// idleTimer closes a socket after a period without reads or writes (Jetty's idle
// timeout, web.timeout).
type idleTimer struct {
	d time.Duration
	t *time.Timer
}

func newIdleTimer(d time.Duration, f func()) *idleTimer {
	if d <= 0 {
		return &idleTimer{}
	}
	return &idleTimer{d: d, t: time.AfterFunc(d, f)}
}

func (i *idleTimer) reset() {
	if i.t != nil {
		i.t.Reset(i.d)
	}
}

func (i *idleTimer) stop() {
	if i.t != nil {
		i.t.Stop()
	}
}
