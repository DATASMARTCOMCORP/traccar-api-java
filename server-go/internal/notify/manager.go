package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/live"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Manager is NotificationManager: what happens to each stored event.
type Manager struct {
	st        *store.Store
	log       *slog.Logger
	format    *Formatter
	forwarder *forwarder // nil without event.forward.url

	types            map[string]bool // notificator.types
	notificators     map[string]notificator
	geocodeOnRequest bool // geocoder.enable, pluscodes and geocoder.onRequest
	timeThreshold    time.Duration
	blockedUsers     map[int64]bool

	// Hooks into the rest of the server, set by NewManager's caller.
	Hooks Hooks
}

// Hooks are what notifications need from the device side.
type Hooks struct {
	// Calendar is Calendar.checkMoment for a calendar id (an error fails the event).
	Calendar func(ctx context.Context, calendarID int64, at time.Time) (bool, error)
	// SendCommand sends a command to a connected device (false: not connected).
	SendCommand func(ctx context.Context, c *model.Command) (bool, error)
	// MailSent and SMSSent count for tc_statistics (StatisticsManager).
	MailSent, SMSSent func()
}

// NewManager reads NotificationManager's, the notificators' and the forwarder's settings.
func NewManager(cfg *config.Config, st *store.Store, hub *live.Hub, log *slog.Logger) (*Manager, error) {
	threshold, err := cfg.Int64("notificator.timeThreshold", 15*60*1000)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		st:            st,
		log:           log,
		timeThreshold: time.Duration(threshold) * time.Millisecond,
		types:         map[string]bool{},
		blockedUsers:  map[int64]bool{},
		format: &Formatter{
			Root:         cfg.String("templates.root", "templates"),
			Translations: cfg.String("web.localizationPath", "./templates/translations"),
			WebURL:       webURL(cfg),
			Zone:         time.Local,
		},
	}
	if v, ok := cfg.Lookup("notification.block.users"); ok {
		for _, s := range strings.Split(v, ",") {
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("notification.block.users: %w", err)
			}
			m.blockedUsers[id] = true
		}
	}
	if v, ok := cfg.Lookup("notificator.types"); ok {
		for _, t := range strings.Split(v, ",") {
			m.types[t] = true
		}
	}
	if cfg.Bool("geocoder.enable", true) {
		switch t := cfg.String("geocoder.type", "locationiq"); t {
		case "pluscodes":
			m.geocodeOnRequest = cfg.Bool("geocoder.onRequest", true)
		default:
			log.Warn("geocoder not ported; notifications keep positions without an address", "geocoder.type", t)
		}
	}
	if m.forwarder, err = newForwarder(cfg, log); err != nil {
		return nil, err
	}
	mail, err := newMailer(cfg, log)
	if err != nil {
		return nil, err
	}
	sms, err := newSMSClient(cfg)
	if err != nil {
		return nil, err
	}
	m.notificators = map[string]notificator{
		"web":     &webNotificator{format: m.format, hub: hub},
		"mail":    &mailNotificator{format: m.format, mail: mail, sent: func() { m.count(m.Hooks.MailSent) }},
		"sms":     &smsNotificator{format: m.format, sms: sms, sent: func() { m.count(m.Hooks.SMSSent) }},
		"command": &commandNotificator{st: st, deliver: func(ctx context.Context, c *model.Command) (bool, error) { return m.sendCommand(ctx, c) }},
	}
	for t := range m.types {
		if _, ok := m.notificators[t]; !ok {
			log.Warn("notificator not ported; notifications through it fail", "type", t)
		}
	}
	return m, nil
}

func (m *Manager) count(f func()) {
	if f != nil {
		f()
	}
}

func (m *Manager) sendCommand(ctx context.Context, c *model.Command) (bool, error) {
	if m.Hooks.SendCommand == nil {
		return false, nil
	}
	return m.Hooks.SendCommand(ctx, c)
}

// SetToken sets how $token is made (TokenManager.generateToken).
func (m *Manager) SetToken(f func(userID int64) (string, error)) { m.format.Token = f }

// webURL is WebHelper.retrieveWebUrl.
func webURL(cfg *config.Config) string {
	if v, ok := cfg.Lookup("web.url"); ok {
		return strings.TrimSuffix(v, "/")
	}
	address := cfg.String("web.address", localAddress())
	port := cfg.String("web.port", strconv.Itoa(config.DefaultWebPort))
	if strings.Contains(address, ":") {
		address = "[" + address + "]"
	}
	return "http://" + address + ":" + port
}

// Event is NotificationManager.updateEvent after the event row is stored (its id set, or
// 0 when the insert failed): forwarding, then the notifications that apply. device is
// the cached Device (its status and positionId as Java's cache has them).
//
// An error is where Java throws out of updateEvents (a lookup failing, or a notificator
// type that isn't enabled): the event handler that detected the event stops there.
// Failed sends are only logged, as Java's failed futures are.
func (m *Manager) Event(ctx context.Context, e *model.Event, pos *model.Position, device model.Device) error {
	if m == nil {
		return nil
	}
	dc := &eventContext{m: m, ctx: ctx, device: device}
	// Java posts the event here and serializes it on the client's thread, so whether the
	// address geocoded below for notifications reaches the payload is a race there; the
	// payload is built now, as when that thread is idle.
	m.forward(dc, e, pos)

	if time.Since(e.EventTime.Time) > m.timeThreshold {
		m.log.Info("Skipping notifications for old event")
		return nil
	}

	notifications, err := dc.matching(e)
	if err != nil {
		return err
	}
	m.log.Info(fmt.Sprintf("Event id: %s, time: %s, type: %s, notifications: %d",
		device.UniqueID, e.EventTime.Time.UTC().Format("2006-01-02 15:04:05"), e.Type, len(notifications)))
	if len(notifications) == 0 {
		return nil
	}

	if pos != nil && pos.Address == nil && m.geocodeOnRequest {
		address := geo.PlusCode(pos.Latitude, pos.Longitude)
		pos.Address = &address
	}

	servers, err := store.All[model.Server](ctx, m.st)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return errors.New("no server row")
	}
	in, err := dc.input(e, pos, &servers[0])
	if err != nil {
		return err
	}
	for _, n := range notifications {
		users, err := dc.notificationUsers(n.ID)
		if err != nil {
			return err
		}
		for _, u := range users {
			if m.blockedUsers[u.ID] {
				m.log.Info(fmt.Sprintf("User %d notification blocked", u.ID))
				continue
			}
			for _, t := range notificatorTypes(n) {
				nt, ok := m.notificators[t]
				if !ok || !m.types[t] {
					// NotificatorManager.getNotificator throws: the rest of the event's
					// notifications are lost.
					return errors.New("Failed to get notificator " + t)
				}
				in := in
				in.Notification, in.User = &n, &u
				if err := nt.send(ctx, in); err != nil {
					m.log.Warn("Notification failed", "type", t, "userId", u.ID, "error", err)
				}
			}
		}
	}
	return nil
}

// notificatorTypes is Notification.getNotificatorsTypes: trimmed names in a HashSet, in
// its iteration order (which decides what is sent before an unknown type fails).
func notificatorTypes(n model.Notification) []string {
	if n.Notificators == nil {
		return nil
	}
	set := model.NewHashSet()
	for _, t := range strings.Split(*n.Notificators, ",") {
		set.Add(strings.TrimSpace(t))
	}
	return set.Items()
}

// eventContext reads, for one event, what Java's CacheManager graph holds for the device.
type eventContext struct {
	m      *Manager
	ctx    context.Context
	device model.Device

	groups []int64 // the device's group chain
	users  []int64 // users linked to the device or its groups (getDeviceObjects(User))
	loaded bool
}

func (c *eventContext) load() error {
	if c.loaded {
		return nil
	}
	seen := map[int64]bool{}
	for id := c.device.GroupID; id > 0 && !seen[id]; {
		seen[id] = true
		g, err := store.Get[model.Group](c.ctx, c.m.st, id)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			return err
		}
		c.groups = append(c.groups, id)
		id = g.GroupID
	}
	users, err := c.linked(store.KindUser, store.KindDevice, c.device.ID)
	if err != nil {
		return err
	}
	for _, g := range c.groups {
		more, err := c.linked(store.KindUser, store.KindGroup, g)
		if err != nil {
			return err
		}
		users = append(users, more...)
	}
	slices.Sort(users)
	c.users = slices.Compact(users)
	c.loaded = true
	return nil
}

// linked returns the owners of kind owner linked to property id (or, with owner and
// property swapped by the caller, the properties of an owner).
func (c *eventContext) linked(owner, property store.Kind, propertyID int64) ([]int64, error) {
	rows, err := c.m.st.LinkRows(c.ctx, owner, 0, property, propertyID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.OwnerID
	}
	return out, nil
}

func (c *eventContext) owned(owner store.Kind, ownerID int64, property store.Kind) ([]int64, error) {
	rows, err := c.m.st.LinkRows(c.ctx, owner, ownerID, property, 0)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.PropertyID
	}
	return out, nil
}

// deviceNotifications is CacheManager.getDeviceNotifications: notifications linked to the
// device or its groups, and those of its users that are marked always.
func (c *eventContext) deviceNotifications() ([]model.Notification, error) {
	if err := c.load(); err != nil {
		return nil, err
	}
	direct, err := c.owned(store.KindDevice, c.device.ID, store.KindNotification)
	if err != nil {
		return nil, err
	}
	for _, g := range c.groups {
		more, err := c.owned(store.KindGroup, g, store.KindNotification)
		if err != nil {
			return nil, err
		}
		direct = append(direct, more...)
	}
	all := slices.Clone(direct)
	for _, u := range c.users {
		more, err := c.owned(store.KindUser, u, store.KindNotification)
		if err != nil {
			return nil, err
		}
		all = append(all, more...)
	}
	slices.Sort(all)
	all = slices.Compact(all)
	var out []model.Notification
	for _, id := range all {
		n, err := store.Get[model.Notification](c.ctx, c.m.st, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if n.Always || slices.Contains(direct, id) {
			out = append(out, n)
		}
	}
	return out, nil
}

// matching is updateEvent's filters: the type, alarms, geofences and calendar.
func (c *eventContext) matching(e *model.Event) ([]model.Notification, error) {
	all, err := c.deviceNotifications()
	if err != nil {
		return nil, err
	}
	var out []model.Notification
	for _, n := range all {
		if n.Type != e.Type {
			continue
		}
		if e.Type == model.EventAlarm {
			alarms := n.Attributes.JavaString("alarms")
			if alarms == nil {
				continue
			}
			alarm := e.Attributes.JavaString(model.KeyAlarm)
			if alarm == nil || !slices.Contains(javaSplit(*alarms, ","), *alarm) {
				continue
			}
		}
		if ids := n.Attributes.JavaString("geofenceIds"); ids != nil {
			match := false
			for _, s := range javaSplit(*ids, ",") {
				id, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("geofenceIds: %w", err)
				}
				if id == e.GeofenceID {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if n.CalendarID != 0 && c.m.Hooks.Calendar != nil {
			ok, err := c.m.Hooks.Calendar(c.ctx, n.CalendarID, e.EventTime.Time)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, n)
	}
	return out, nil
}

// notificationUsers is CacheManager.getNotificationUsers: the device's users linked to
// the notification.
func (c *eventContext) notificationUsers(notificationID int64) ([]model.User, error) {
	ids, err := c.linked(store.KindUser, store.KindNotification, notificationID)
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	var out []model.User
	for _, id := range slices.Compact(ids) {
		if !slices.Contains(c.users, id) {
			continue
		}
		u, err := store.Get[model.User](c.ctx, c.m.st, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// input reads the formatter's objects: the event's geofence, maintenance and driver.
func (c *eventContext) input(e *model.Event, pos *model.Position, server *model.Server) (Input, error) {
	device := c.device
	in := Input{Server: server, Device: &device, Event: e, Position: pos}
	if e.GeofenceID != 0 {
		g, err := store.Get[model.Geofence](c.ctx, c.m.st, e.GeofenceID)
		switch {
		case err == nil:
			in.Geofence = &g
		case !errors.Is(err, store.ErrNotFound):
			return in, err
		}
	}
	if e.MaintenanceID != 0 {
		mt, err := store.Get[model.Maintenance](c.ctx, c.m.st, e.MaintenanceID)
		switch {
		case err == nil:
			in.Maintenance = &mt
		case !errors.Is(err, store.ErrNotFound):
			return in, err
		}
	}
	if id := e.Attributes.JavaString(model.KeyDriverUniqueID); id != nil {
		in.DriverSet = true
		d, err := c.driver(*id)
		if err != nil {
			return in, err
		}
		in.Driver = d
	}
	return in, nil
}

// driver is the device's linked driver (directly or through its groups) with a unique id.
func (c *eventContext) driver(uniqueID string) (*model.Driver, error) {
	if err := c.load(); err != nil {
		return nil, err
	}
	ids, err := c.owned(store.KindDevice, c.device.ID, store.KindDriver)
	if err != nil {
		return nil, err
	}
	for _, g := range c.groups {
		more, err := c.owned(store.KindGroup, g, store.KindDriver)
		if err != nil {
			return nil, err
		}
		ids = append(ids, more...)
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		d, err := store.Get[model.Driver](c.ctx, c.m.st, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if d.UniqueID == uniqueID {
			return &d, nil
		}
	}
	return nil, nil
}

// javaSplit is String.split(literal): trailing empty strings dropped.
func javaSplit(s, sep string) []string {
	parts := strings.Split(s, sep)
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
