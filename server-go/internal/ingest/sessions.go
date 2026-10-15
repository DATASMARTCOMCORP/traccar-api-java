package ingest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// endpoint is Java's ConnectionKey: a connection plus the remote address. A TCP or HTTP
// connection has one; a UDP socket has one per sender.
type endpoint struct {
	conn   *Conn
	remote string
}

// DeviceSession is a device identified on an endpoint (Java's DeviceSession).
type DeviceSession struct {
	DeviceID int64
	UniqueID string
	Model    *string
	endpoint endpoint

	mu         sync.Mutex
	lastUpdate time.Time
	locals     map[string]any
}

// Get and Set are DeviceSession.get/set: per-device decoder state kept with the session
// (a nil value removes the key).
func (s *DeviceSession) Get(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locals[key]
}

func (s *DeviceSession) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == nil {
		delete(s.locals, key)
		return
	}
	if s.locals == nil {
		s.locals = map[string]any{}
	}
	s.locals[key] = value
}

func (s *DeviceSession) touch() {
	s.mu.Lock()
	s.lastUpdate = time.Now()
	s.mu.Unlock()
}

// sessions is Java's ConnectionManager: which device is on which endpoint, device
// status (online, offline, unknown) with its timeout, and unknown-device bookkeeping.
type sessions struct {
	p *Processor

	mu         sync.Mutex
	byDevice   map[int64]*DeviceSession
	byEndpoint map[endpoint]*endpointSessions
	unknown    map[endpoint]unknownEntry
	lastSeen   map[int64]time.Time

	throttle map[string]*throttleInfo // DeviceLookupService, with database.throttleUnknown
}

// endpointSessions are the devices of one endpoint, in the iteration order of Java's
// ConcurrentHashMap keyed by unique id (findAny takes the first).
type endpointSessions struct {
	order *model.HashSet
	byID  map[string]*DeviceSession
}

func (e *endpointSessions) first() *DeviceSession {
	if items := e.order.Items(); len(items) > 0 {
		return e.byID[items[0]]
	}
	return nil
}

func (e *endpointSessions) remove(uniqueID string) {
	e.order.Remove(uniqueID)
	delete(e.byID, uniqueID)
}

type unknownEntry struct {
	uniqueID string
	at       time.Time
}

func newSessions(p *Processor) *sessions {
	return &sessions{
		p:          p,
		byDevice:   map[int64]*DeviceSession{},
		byEndpoint: map[endpoint]*endpointSessions{},
		unknown:    map[endpoint]unknownEntry{},
		lastSeen:   map[int64]time.Time{},
		throttle:   map[string]*throttleInfo{},
	}
}

// errDisabled is Device.checkDisabled's SecurityException: Java's decoder then throws and
// the connection is closed.
var errDisabled = errors.New("device is disabled or expired")

// deviceSession is ConnectionManager.getDeviceSession. With unique ids it finds or
// creates the session of the first id that is a known device (registering an unknown
// device when database.registerUnknown allows it); without ids it returns any session
// already on the endpoint. A nil session means the device is unknown.
func (s *sessions) deviceSession(ctx context.Context, ep endpoint, uniqueIDs ...string) (*DeviceSession, error) {
	s.mu.Lock()
	es := s.byEndpoint[ep]
	if len(uniqueIDs) == 0 {
		defer s.mu.Unlock()
		if es == nil {
			return nil, nil
		}
		ds := es.first()
		if ds != nil {
			ds.touch()
		}
		return ds, nil
	}
	if es != nil {
		for _, id := range uniqueIDs {
			if ds := es.byID[id]; ds != nil {
				s.mu.Unlock()
				ds.touch()
				return ds, nil
			}
		}
	}
	s.mu.Unlock()

	device, err := s.lookup(ctx, uniqueIDs)
	if err != nil {
		return nil, err
	}
	first := uniqueIDs[0]
	if device == nil && s.p.registerUnknown && s.p.registerUnknownRegex.MatchString(first) {
		device = s.addUnknownDevice(ctx, first)
	}
	if device == nil {
		s.mu.Lock()
		s.unknown[ep] = unknownEntry{first, time.Now()}
		s.mu.Unlock()
		s.p.log.Warn("Unknown device - "+strings.Join(uniqueIDs, " ")+" ("+hostString(ep.remote)+")", "protocol", ep.conn.protocol)
		return nil, nil
	}

	s.mu.Lock()
	delete(s.unknown, ep)
	s.mu.Unlock()
	if device.Disabled || !device.ExpirationTime.IsZero() && time.Now().After(device.ExpirationTime.Time) {
		return nil, fmt.Errorf("device %s: %w", device.UniqueID, errDisabled)
	}

	s.mu.Lock()
	old := s.byDevice[device.ID]
	delete(s.byDevice, device.ID)
	if old != nil {
		if oes := s.byEndpoint[old.endpoint]; oes != nil && len(oes.byID) > 1 {
			oes.remove(device.UniqueID)
		} else {
			delete(s.byEndpoint, old.endpoint)
		}
	}
	ds := &DeviceSession{DeviceID: device.ID, UniqueID: device.UniqueID, Model: device.Model, endpoint: ep, lastUpdate: time.Now()}
	es = s.byEndpoint[ep]
	if es == nil {
		es = &endpointSessions{order: model.NewHashSet(), byID: map[string]*DeviceSession{}}
		s.byEndpoint[ep] = es
	}
	es.order.Add(device.UniqueID)
	es.byID[device.UniqueID] = ds
	s.byDevice[device.ID] = ds
	s.mu.Unlock()

	if err := s.p.cache.addDevice(ctx, device.ID, ep); err != nil {
		return nil, err
	}
	if old != nil {
		s.p.cache.removeDevice(device.ID, old.endpoint)
	}
	return ds, nil
}

// session is ConnectionManager.getDeviceSession(deviceId).
func (s *sessions) session(deviceID int64) *DeviceSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byDevice[deviceID]
}

// hostString is InetSocketAddress.getHostString for a "host:port" address.
func hostString(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return strings.Trim(addr[:i], "[]")
	}
	return addr
}

// addUnknownDevice is ConnectionManager.addUnknownDevice. A failed insert is logged and
// the device stays unknown.
func (s *sessions) addUnknownDevice(ctx context.Context, uniqueID string) *model.Device {
	d := model.Device{Name: uniqueID, UniqueID: uniqueID, Category: s.p.registerUnknownCategory, GroupID: s.p.registerUnknownGroupID}
	if err := store.Insert(ctx, s.p.st, &d); err != nil {
		s.p.log.Warn("Automatic registration failed", "error", err)
		return nil
	}
	s.p.log.Info("Automatically registered " + uniqueID)
	return &d
}

// disconnected is ConnectionManager.deviceDisconnected for a closed connection: its
// devices lose their sessions, and go offline when the protocol reports disconnections
// (not HTTP, not status.ignoreOffline protocols).
func (s *sessions) disconnected(ctx context.Context, ep endpoint, supportsOffline bool) {
	s.mu.Lock()
	es := s.byEndpoint[ep]
	delete(s.byEndpoint, ep)
	delete(s.unknown, ep)
	var gone []*DeviceSession
	if es != nil {
		for _, id := range es.order.Items() {
			gone = append(gone, es.byID[id])
		}
	}
	s.mu.Unlock()
	for _, ds := range gone {
		if supportsOffline {
			s.updateDevice(ctx, ds.DeviceID, model.StatusOffline, nil)
		}
		s.mu.Lock()
		delete(s.byDevice, ds.DeviceID)
		s.mu.Unlock()
		s.p.cache.removeDevice(ds.DeviceID, ep)
	}
}

// deviceUnknown is ConnectionManager.deviceUnknown: status "unknown" and no session.
func (s *sessions) deviceUnknown(ctx context.Context, deviceID int64) {
	s.updateDevice(ctx, deviceID, model.StatusUnknown, nil)
	s.mu.Lock()
	delete(s.lastSeen, deviceID)
	ds := s.byDevice[deviceID]
	delete(s.byDevice, deviceID)
	if ds != nil {
		if es := s.byEndpoint[ds.endpoint]; es != nil {
			es.remove(ds.UniqueID)
			if len(es.byID) == 0 {
				delete(s.byEndpoint, ds.endpoint)
			}
		}
	}
	s.mu.Unlock()
	if ds != nil {
		s.p.cache.removeDevice(deviceID, ds.endpoint)
	}
}

// sweep is ConnectionManager.sweepIdleSessions: devices not heard from for status.timeout
// become unknown.
func (s *sessions) sweep(ctx context.Context) {
	cutoff := time.Now().Add(-s.p.statusTimeout)
	s.mu.Lock()
	var idle []int64
	for id, seen := range s.lastSeen {
		if seen.Before(cutoff) {
			idle = append(idle, id)
		}
	}
	for ep, u := range s.unknown {
		if u.at.Before(cutoff) {
			delete(s.unknown, ep)
		}
	}
	s.mu.Unlock()
	for _, id := range idle {
		s.deviceUnknown(ctx, id)
	}
}

// updateDevice is ConnectionManager.updateDevice: it writes status (and lastUpdate when
// at is set), keeps the timeout bookkeeping, and stores a status event when
// event.status.enable is on and the status changed.
func (s *sessions) updateDevice(ctx context.Context, deviceID int64, status string, at *time.Time) {
	d, err := store.Get[model.Device](ctx, s.p.st, deviceID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.p.log.Warn("Failed to get device", "error", err)
		}
		return
	}
	// Java compares with the status of its cached device when the device is cached (the
	// last status it set), otherwise with the column as read, blank-padded (CHAR(8)), so
	// that comparison always sees a change.
	oldStatus, cached := s.p.cache.status(deviceID)
	if !cached {
		oldStatus = model.StatusOffline
		if d.Status != nil {
			oldStatus = *d.Status
		}
	}
	s.p.cache.setStatus(deviceID, status)
	d.Status = &status
	s.mu.Lock()
	if status == model.StatusOnline {
		s.lastSeen[deviceID] = time.Now()
	} else {
		delete(s.lastSeen, deviceID)
	}
	s.mu.Unlock()
	if status != oldStatus && s.p.statusEvents {
		eventType := model.EventDeviceOffline
		switch status {
		case model.StatusOnline:
			eventType = model.EventDeviceOnline
		case model.StatusUnknown:
			eventType = model.EventDeviceUnknown
		}
		s.p.storeEvents(ctx, []*model.Event{newDeviceEvent(eventType, deviceID)}, nil)
	}
	if at != nil {
		d.LastUpdate = model.NewTime(*at)
	}
	if err := s.p.st.UpdateDeviceStatus(ctx, &d); err != nil {
		s.p.log.Warn("Update device status error", "error", err)
	}
}

// throttleInfo is DeviceLookupService's per-identifier backoff for unknown devices.
type throttleInfo struct {
	lastQuery time.Time
	delay     time.Duration
	expires   time.Time
}

const (
	throttleMin  = time.Minute
	throttleMax  = 30 * time.Minute
	throttleKeep = time.Hour
)

// lookup is DeviceLookupService.lookup: the device of the first known unique id.
func (s *sessions) lookup(ctx context.Context, uniqueIDs []string) (*model.Device, error) {
	for _, id := range uniqueIDs {
		if s.throttled(id) {
			s.p.log.Debug("Device lookup throttled " + id)
			continue
		}
		d, err := s.p.st.DeviceByUniqueID(ctx, id)
		switch {
		case err == nil:
			s.lookupDone(id, true)
			return &d, nil
		case errors.Is(err, store.ErrNotFound):
			s.lookupDone(id, false)
		default:
			s.p.log.Warn("Find device error", "error", err)
			return nil, nil
		}
	}
	return nil, nil
}

func (s *sessions) throttled(id string) bool {
	if !s.p.throttleUnknown {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.throttle[id]
	if t != nil && time.Now().After(t.expires) {
		delete(s.throttle, id)
		t = nil
	}
	return t != nil && time.Now().Before(t.lastQuery.Add(t.delay))
}

func (s *sessions) lookupDone(id string, found bool) {
	if !s.p.throttleUnknown {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if found {
		delete(s.throttle, id)
		return
	}
	t := s.throttle[id]
	if t != nil && time.Now().After(t.expires) {
		t = nil
	}
	if t != nil {
		t.delay = min(2*t.delay, throttleMax)
	} else {
		t = &throttleInfo{delay: throttleMin}
		s.throttle[id] = t
	}
	t.lastQuery = time.Now()
	t.expires = t.lastQuery.Add(throttleKeep)
}

// registerUnknownPattern compiles database.registerUnknown.regex for String.matches (the
// whole id must match).
func registerUnknownPattern(expr string) (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + expr + `)$`)
}
