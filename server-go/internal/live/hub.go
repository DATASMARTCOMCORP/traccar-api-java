// Package live pushes devices, positions, events and device logs to the web clients' sockets
// (/api/socket), as ConnectionManager's update listeners and AsyncSocket do.
package live

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// keepalivePeriod is TaskWebSocketKeepalive.PERIOD_SECONDS.
const keepalivePeriod = 55 * time.Second

// Hub is ConnectionManager's listener side: the open sockets by user, and which users see
// which devices. The device sets are read when a user's first socket opens and are only
// extended when that user creates a device, as in Java: links made later through
// permissions or groups reach a user's sockets once all of them have closed and one
// reopens.
//
// A nil Hub does nothing, so ingest and the API can run without one.
type Hub struct {
	st  *store.Store
	log *slog.Logger
	// ShowUnknownDevices is web.showUnknownDevices: logs of connections without a session
	// whose unique id is unknown go to every socket.
	ShowUnknownDevices bool

	mu          sync.Mutex
	listeners   map[int64]map[*Listener]struct{}
	userDevices map[int64]map[int64]struct{}
	deviceUsers map[int64]map[int64]struct{}
}

// NewHub returns an empty hub.
func NewHub(st *store.Store, log *slog.Logger) *Hub {
	return &Hub{
		st:          st,
		log:         log,
		listeners:   map[int64]map[*Listener]struct{}{},
		userDevices: map[int64]map[int64]struct{}{},
		deviceUsers: map[int64]map[int64]struct{}{},
	}
}

// Listener is one socket (AsyncSocket): messages are queued for its writer in order.
type Listener struct {
	out  chan []byte
	mu   sync.Mutex
	logs bool // includeLogs: the client asked for {"logs": true}
	done bool
}

// NewListener returns a listener whose messages arrive on Messages.
func NewListener() *Listener { return &Listener{out: make(chan []byte, 1024)} }

// Messages is the queue of JSON messages to write to the socket.
func (l *Listener) Messages() <-chan []byte { return l.out }

// SetLogs is the client's {"logs": ...} message.
func (l *Listener) SetLogs(on bool) {
	l.mu.Lock()
	l.logs = on
	l.mu.Unlock()
}

// Send queues a message. A client too slow to drain its queue loses the message, as a
// closed Jetty session would (Java sends asynchronously and drops failures).
func (l *Listener) Send(msg []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	select {
	case l.out <- msg:
	default:
	}
}

func (l *Listener) sendLog(msg []byte) {
	l.mu.Lock()
	on := l.logs
	l.mu.Unlock()
	if on {
		l.Send(msg)
	}
}

// close stops the queue; its writer sees the channel closed.
func (l *Listener) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.done {
		l.done = true
		close(l.out)
	}
}

// AddListener is ConnectionManager.addListener: a user's first socket loads the devices
// the user may see.
func (h *Hub) AddListener(ctx context.Context, userID int64, l *Listener) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.listeners[userID]
	if set == nil {
		ids, err := store.IDs[model.Device](ctx, h.st, store.Permission(store.KindUser, userID, store.KindDevice))
		if err != nil {
			return err
		}
		set = map[*Listener]struct{}{}
		h.listeners[userID] = set
		devices := map[int64]struct{}{}
		for _, id := range ids {
			devices[id] = struct{}{}
			users := h.deviceUsers[id]
			if users == nil {
				users = map[int64]struct{}{}
				h.deviceUsers[id] = users
			}
			users[userID] = struct{}{}
		}
		h.userDevices[userID] = devices
	}
	set[l] = struct{}{}
	return nil
}

// RemoveListener is ConnectionManager.removeListener, and closes the listener's queue.
func (h *Hub) RemoveListener(userID int64, l *Listener) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l.close()
	set := h.listeners[userID]
	if set == nil {
		return
	}
	delete(set, l)
	if len(set) > 0 {
		return
	}
	delete(h.listeners, userID)
	for deviceID := range h.userDevices[userID] {
		if users := h.deviceUsers[deviceID]; users != nil {
			delete(users, userID)
			if len(users) == 0 {
				delete(h.deviceUsers, deviceID)
			}
		}
	}
	delete(h.userDevices, userID)
}

// LinkDevice is ConnectionManager.invalidatePermission for a device a user created: when
// the user has sockets, the device becomes theirs, and only theirs: Java replaces the
// device's user set (other users' sockets stop getting it).
func (h *Hub) LinkDevice(userID, deviceID int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.listeners[userID]; !ok {
		return
	}
	h.userDevices[userID][deviceID] = struct{}{}
	h.deviceUsers[deviceID] = map[int64]struct{}{userID: {}}
}

// message is the JSON object AsyncSocket sends: {key: [item]}, or {} when key is "".
func message(key string, item interface{ MarshalJSON() ([]byte, error) }) ([]byte, error) {
	if key == "" {
		return []byte("{}"), nil
	}
	data, err := item.MarshalJSON()
	if err != nil {
		return nil, err
	}
	b := append([]byte(`{"`+key+`":[`), data...)
	return append(b, ']', '}'), nil
}

// toDeviceUsers sends a message to the sockets of the users who see a device.
func (h *Hub) toDeviceUsers(deviceID int64, key string, item interface{ MarshalJSON() ([]byte, error) }, logs bool) {
	msg, err := message(key, item)
	if err != nil {
		h.log.Warn("Socket JSON formatting error", "error", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for userID := range h.deviceUsers[deviceID] {
		for l := range h.listeners[userID] {
			if logs {
				l.sendLog(msg)
			} else {
				l.Send(msg)
			}
		}
	}
}

// UpdateDevice is ConnectionManager.updateDevice(local, device).
func (h *Hub) UpdateDevice(d model.Device) {
	if h == nil {
		return
	}
	h.toDeviceUsers(d.ID, "devices", d, false)
}

// UpdatePosition is ConnectionManager.updatePosition.
func (h *Hub) UpdatePosition(p *model.Position) {
	if h == nil {
		return
	}
	h.toDeviceUsers(p.DeviceID, "positions", *p, false)
}

// UpdateEvent is ConnectionManager.updateEvent: an event for one user's sockets (the web
// notificator).
func (h *Hub) UpdateEvent(userID int64, e model.Event) {
	if h == nil {
		return
	}
	msg, err := message("events", e)
	if err != nil {
		h.log.Warn("Socket JSON formatting error", "error", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for l := range h.listeners[userID] {
		l.Send(msg)
	}
}

// LogDevice sends a log record of a connection with a device session to the sockets that
// see the device and asked for logs.
func (h *Hub) LogDevice(r model.LogRecord) {
	if h == nil {
		return
	}
	h.toDeviceUsers(r.DeviceID, "logs", r, true)
}

// LogUnknown sends a log record of a connection of an unknown device to every socket that
// asked for logs, when web.showUnknownDevices is on.
func (h *Hub) LogUnknown(r model.LogRecord) {
	if h == nil || !h.ShowUnknownDevices {
		return
	}
	msg, err := message("logs", r)
	if err != nil {
		h.log.Warn("Socket JSON formatting error", "error", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.listeners {
		for l := range set {
			l.sendLog(msg)
		}
	}
}

// Keepalive is ConnectionManager.sendKeepalive: {} to every socket.
func (h *Hub) Keepalive() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.listeners {
		for l := range set {
			l.Send([]byte("{}"))
		}
	}
}

// Run sends keepalives every 55 seconds until ctx ends (TaskWebSocketKeepalive).
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(keepalivePeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Keepalive()
		}
	}
}
