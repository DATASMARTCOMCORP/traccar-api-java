package api

import (
	"errors"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Read-only endpoints (MIGRATION.md Step 4), with the parameters and permission rules of
// Java's resources.

type jsonMarshaler interface{ MarshalJSON() ([]byte, error) }

// writeList writes a JSON array of entities.
func writeList[T jsonMarshaler](w http.ResponseWriter, items []T) error {
	return writeListStatus(w, http.StatusOK, items)
}

func writeListStatus[T jsonMarshaler](w http.ResponseWriter, status int, items []T) error {
	b := []byte{'['}
	for i, it := range items {
		if i > 0 {
			b = append(b, ',')
		}
		data, err := it.MarshalJSON()
		if err != nil {
			return err
		}
		b = append(b, data...)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := w.Write(append(b, ']'))
	return err
}

// errNoEntity is a 404 a Java resource returns without a body; Jetty then renders its
// HTML error page, so that is the Content-Type clients get.
var errNoEntity = &httpError{status: http.StatusNotFound, msg: "Not Found", contentType: jettyErrorPage}

// collection describes a resource built on Java's SimpleObjectResource (all, userId) or
// ExtendedObjectResource (also groupId and deviceId).
type collection struct {
	kind     store.Kind
	sort     string
	search   []string
	extended bool
}

func (a *API) readRoutes() {
	registerCollection[model.Group](a, "/api/groups", collection{store.KindGroup, "name", []string{"name"}, false})
	registerCollection[model.Calendar](a, "/api/calendars", collection{store.KindCalendar, "name", []string{"name"}, false})
	registerCollection[model.Order](a, "/api/orders", collection{store.KindOrder, "description", []string{"description"}, false})
	registerCollection[model.Report](a, "/api/reports", collection{store.KindReport, "description", []string{"description"}, false})
	registerCollection[model.Geofence](a, "/api/geofences", collection{store.KindGeofence, "name", []string{"name"}, true})
	registerCollection[model.Notification](a, "/api/notifications", collection{store.KindNotification, "description", []string{"description"}, true})
	registerCollection[model.Driver](a, "/api/drivers", collection{store.KindDriver, "name", []string{"name", "uniqueId"}, true})
	registerCollection[model.ComputedAttribute](a, "/api/attributes/computed", collection{store.KindAttribute, "description", []string{"description"}, true})
	registerCollection[model.Command](a, "/api/commands", collection{store.KindCommand, "description", []string{"description"}, true})
	registerCollection[model.Maintenance](a, "/api/maintenance", collection{store.KindMaintenance, "name", []string{"name"}, true})

	a.handle("GET /api/devices", authenticated, a.getDevices)
	a.handle("GET /api/devices/{id}", authenticated, single[model.Device](a, store.KindDevice))
	a.handle("GET /api/users", authenticated, a.getUsers)
	a.handle("GET /api/users/{id}", authenticated, single[model.User](a, store.KindUser))

	a.handle("GET /api/positions", authenticated, a.getPositions)
	a.handle("GET /api/events/{id}", authenticated, a.getEvent)
	a.handle("GET /api/statistics", authenticated, a.getStatistics)
	a.handle("GET /api/audit", authenticated, a.getAudit)
	a.handle("GET /api/permissions", authenticated, a.getPermissions)
	a.handle("GET /api/commands/types", authenticated, a.getCommandTypes)
	a.handle("GET /api/commands/send", authenticated, a.getSendableCommands)
	a.handle("GET /api/notifications/types", authenticated, getNotificationTypes)
	a.handle("GET /api/notifications/notificators", authenticated, a.getNotificators)
	a.handle("GET /api/server/timezones", authenticated, getTimezones)
	a.handle("GET /api/server/gc", authenticated, getGC)
	a.handleText("GET /api/health", a.getHealth)
}

func registerCollection[T interface {
	store.Entity
	jsonMarshaler
}](a *API, path string, c collection) {
	a.handle("GET "+path, authenticated, func(w http.ResponseWriter, r *request) error {
		where, err := a.ownership(r, c)
		if err != nil {
			return err
		}
		o, err := listOptions(r, c.sort, c.search, where)
		if err != nil {
			return err
		}
		items, err := store.List[T](r.Context(), a.st, o)
		if err != nil {
			return err
		}
		return writeList(w, items)
	})
	a.handle("GET "+path+"/{id}", authenticated, single[T](a, c.kind))
}

// ownership builds the visibility conditions of SimpleObjectResource and
// ExtendedObjectResource.get.
func (a *API) ownership(r *request, c collection) ([]store.Cond, error) {
	userID, err := qInt64(r, "userId")
	if err != nil {
		return nil, err
	}
	var where []store.Cond
	switch {
	case qBool(r, "all"):
		if !r.user.Administrator {
			where = append(where, store.Permission(store.KindUser, r.user.ID, c.kind))
		}
	case userID == 0:
		where = append(where, store.Permission(store.KindUser, r.user.ID, c.kind))
	default:
		if err := a.checkUser(r, userID); err != nil {
			return nil, err
		}
		if c.extended {
			where = append(where, store.PermissionNoGroups(store.KindUser, userID, c.kind))
		} else {
			where = append(where, store.Permission(store.KindUser, userID, c.kind))
		}
	}
	if !c.extended {
		return where, nil
	}
	for _, owner := range []store.Kind{store.KindGroup, store.KindDevice} {
		id, err := qInt64(r, owner.Key())
		if err != nil {
			return nil, err
		}
		if id > 0 {
			if err := a.checkPermission(r, owner, id); err != nil {
				return nil, err
			}
			where = append(where, store.PermissionNoGroups(owner, id, c.kind))
		}
	}
	return where, nil
}

// listOptions reads keyword, limit, offset and excludeAttributes.
func listOptions(r *request, sort string, search []string, where []store.Cond) (store.ListOptions, error) {
	limit, err := qInt(r, "limit")
	if err != nil {
		return store.ListOptions{}, err
	}
	offset, err := qInt(r, "offset")
	if err != nil {
		return store.ListOptions{}, err
	}
	if kw := r.URL.Query().Get("keyword"); kw != "" {
		where = append(where, store.Contains(kw, search...))
	}
	return store.ListOptions{
		Where: where, OrderBy: sort, Limit: limit, Offset: offset,
		ExcludeAttributes: qBool(r, "excludeAttributes"),
	}, nil
}

// single is BaseObjectResource.getSingle: permission first, then the row or a 404.
func single[T interface {
	store.Entity
	jsonMarshaler
}](a *API, kind store.Kind) handlerFunc {
	return func(w http.ResponseWriter, r *request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := a.checkPermission(r, kind, id); err != nil {
			return err
		}
		v, err := store.Get[T](r.Context(), a.st, id)
		if errors.Is(err, store.ErrNotFound) {
			return errNoEntity
		}
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, v)
	}
}

func pathID(r *request) (int64, error) {
	id, err := parseInt64(r.PathValue("id"))
	if err != nil {
		return 0, errNotFound // Jersey: unconvertible path parameter
	}
	return id, nil
}

// getDevices is DeviceResource.get: by uniqueId/id lists, or by ownership with an optional
// linked-device filter, sorted by name.
func (a *API) getDevices(w http.ResponseWriter, r *request) error {
	q := r.URL.Query()
	uniqueIDs := q["uniqueId"]
	ids, err := qInt64s(r, "id")
	if err != nil {
		return err
	}
	exclude := qBool(r, "excludeAttributes")
	if len(uniqueIDs) > 0 || len(ids) > 0 {
		var result []model.Device
		perm := store.Permission(store.KindUser, r.user.ID, store.KindDevice)
		for _, u := range uniqueIDs {
			d, err := store.List[model.Device](r.Context(), a.st, store.ListOptions{
				Where: []store.Cond{store.Equals("uniqueId", u), perm}, ExcludeAttributes: exclude})
			if err != nil {
				return err
			}
			result = append(result, d...)
		}
		for _, id := range ids {
			d, err := store.List[model.Device](r.Context(), a.st, store.ListOptions{
				Where: []store.Cond{store.Equals("id", id), perm}, ExcludeAttributes: exclude})
			if err != nil {
				return err
			}
			result = append(result, d...)
		}
		return writeList(w, result)
	}
	// Devices use deviceId for "linked from", not ownership (see deviceOwnership).
	where, err := a.deviceOwnership(r)
	if err != nil {
		return err
	}
	o, err := listOptions(r, "name", []string{"name", "uniqueId", "phone", "model", "contact"}, where)
	if err != nil {
		return err
	}
	devices, err := store.List[model.Device](r.Context(), a.st, o)
	if err != nil {
		return err
	}
	return writeList(w, devices)
}

func (a *API) deviceOwnership(r *request) ([]store.Cond, error) {
	userID, err := qInt64(r, "userId")
	if err != nil {
		return nil, err
	}
	var where []store.Cond
	switch {
	case qBool(r, "all"):
		if !r.user.Administrator {
			where = append(where, store.Permission(store.KindUser, r.user.ID, store.KindDevice))
		}
	case userID == 0:
		where = append(where, store.Permission(store.KindUser, r.user.ID, store.KindDevice))
	default:
		if err := a.checkUser(r, userID); err != nil {
			return nil, err
		}
		where = append(where, store.PermissionNoGroups(store.KindUser, userID, store.KindDevice))
	}
	linkedFrom, err := qInt64(r, "deviceId")
	if err != nil {
		return nil, err
	}
	if linkedFrom > 0 {
		if err := a.checkPermission(r, store.KindDevice, linkedFrom); err != nil {
			return nil, err
		}
		where = append(where, store.PermissionNoGroups(store.KindDevice, linkedFrom, store.KindLinkedDevice))
	}
	return where, nil
}

// getUsers is UserResource.get: managed users (all users for administrators), sorted by
// name.
func (a *API) getUsers(w http.ResponseWriter, r *request) error {
	userID, err := qInt64(r, "userId")
	if err != nil {
		return err
	}
	deviceID, err := qInt64(r, "deviceId")
	if err != nil {
		return err
	}
	var where []store.Cond
	if userID > 0 {
		if err := a.checkUser(r, userID); err != nil {
			return err
		}
		where = append(where, store.PermissionNoGroups(store.KindUser, userID, store.KindManagedUser))
	} else if !r.user.Administrator {
		where = append(where, store.PermissionNoGroups(store.KindUser, r.user.ID, store.KindManagedUser))
	}
	if deviceID > 0 {
		if err := checkManager(r); err != nil {
			return err
		}
		if err := a.checkPermission(r, store.KindDevice, deviceID); err != nil {
			return err
		}
		where = append(where, store.PermissionOf(store.KindUser, store.KindDevice, deviceID, true))
	}
	o, err := listOptions(r, "name", []string{"name", "email"}, where)
	if err != nil {
		return err
	}
	users, err := store.List[model.User](r.Context(), a.st, o)
	if err != nil {
		return err
	}
	return writeList(w, users)
}

// Defaults of keys the position endpoints read.
const (
	defaultPositionPeriod = 7776000 * time.Second // database.positionPeriod: 90 days
	defaultMaxPositions   = 50000                 // report.maxPositions
)

// getPositions is PositionResource.getJson.
func (a *API) getPositions(w http.ResponseWriter, r *request) error {
	ids, err := qInt64s(r, "id")
	if err != nil {
		return err
	}
	deviceID, err := qInt64(r, "deviceId")
	if err != nil {
		return err
	}
	geofenceID, err := qInt64(r, "geofenceId")
	if err != nil {
		return err
	}
	from, err := qDate(r, "from")
	if err != nil {
		return err
	}
	to, err := qDate(r, "to")
	if err != nil {
		return err
	}
	switch {
	case len(ids) > 0:
		var positions []model.Position
		for _, id := range ids {
			p, err := store.Get[model.Position](r.Context(), a.st, id)
			if err != nil {
				return err // a missing position is a NullPointerException in Java: 400
			}
			if err := a.checkPermission(r, store.KindDevice, p.DeviceID); err != nil {
				return err
			}
			positions = append(positions, p)
		}
		return writeList(w, positions)
	case deviceID > 0:
		if err := a.checkPermission(r, store.KindDevice, deviceID); err != nil {
			return err
		}
		if !from.IsZero() && !to.IsZero() {
			if err := a.checkRestriction(r, func(x restrictions) bool { return x.disableReports }); err != nil {
				return err
			}
			// The geofence isn't permission-checked, and an unknown one filters nothing.
			var geofence *model.Geofence
			if geofenceID != 0 {
				g, err := store.Get[model.Geofence](r.Context(), a.st, geofenceID)
				switch {
				case err == nil:
					geofence = &g
				case !errors.Is(err, store.ErrNotFound):
					return err
				}
			}
			positions, err := a.st.Positions(r.Context(), deviceID, from, to, a.opts.MaxPositions)
			if err != nil {
				return err
			}
			if geofence != nil {
				var inside []model.Position
				for _, p := range positions {
					in, err := geo.GeofenceContains(geofence, p.Latitude, p.Longitude, p.Altitude)
					if err != nil {
						return err
					}
					if in {
						inside = append(inside, p)
					}
				}
				positions = inside
			}
			return writeList(w, positions)
		}
		positions, err := a.st.LatestPositions(r.Context(), deviceID, 0)
		if err != nil {
			return err
		}
		return writeList(w, positions)
	default:
		// PositionUtil.getLatestPositions: latest positions, kept when the user sees the device.
		visible, err := store.IDs[model.Device](r.Context(), a.st, store.Permission(store.KindUser, r.user.ID, store.KindDevice))
		if err != nil {
			return err
		}
		positions, err := a.st.LatestPositions(r.Context(), 0, a.opts.PositionPeriod)
		if err != nil {
			return err
		}
		positions = slices.DeleteFunc(positions, func(p model.Position) bool { return !slices.Contains(visible, p.DeviceID) })
		return writeList(w, positions)
	}
}

// getEvent is EventResource.get: 404 when missing, then the device permission.
func (a *API) getEvent(w http.ResponseWriter, r *request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	e, err := store.Get[model.Event](r.Context(), a.st, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindDevice, e.DeviceID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, e)
}

// betweenDates reads the from/to parameters an admin listing needs. Java passes absent
// dates to the query as null, which fails (400).
func betweenDates(r *request) (model.Time, model.Time, error) {
	from, err := qDate(r, "from")
	if err != nil {
		return model.Time{}, model.Time{}, err
	}
	to, err := qDate(r, "to")
	if err != nil {
		return model.Time{}, model.Time{}, err
	}
	if from.IsZero() || to.IsZero() {
		return model.Time{}, model.Time{}, errors.New("from and to are required")
	}
	return from, to, nil
}

func (a *API) getStatistics(w http.ResponseWriter, r *request) error {
	if err := checkAdmin(r); err != nil {
		return err
	}
	from, to, err := betweenDates(r)
	if err != nil {
		return err
	}
	stats, err := store.List[model.Statistics](r.Context(), a.st, store.ListOptions{
		Where: []store.Cond{a.st.Between("captureTime", from, to)}, OrderBy: "captureTime"})
	if err != nil {
		return err
	}
	return writeList(w, stats)
}

// getAudit is AuditResource.get: actions in a period with each user's e-mail.
func (a *API) getAudit(w http.ResponseWriter, r *request) error {
	if err := checkAdmin(r); err != nil {
		return err
	}
	from, to, err := betweenDates(r)
	if err != nil {
		return err
	}
	users, err := store.All[model.User](r.Context(), a.st)
	if err != nil {
		return err
	}
	emails := map[int64]string{}
	for _, u := range users {
		emails[u.ID] = u.Email
	}
	actions, err := store.List[model.Action](r.Context(), a.st, store.ListOptions{
		Where: []store.Cond{a.st.Between("actionTime", from, to)}, OrderBy: "actionTime"})
	if err != nil {
		return err
	}
	entries := make([]model.AuditEntry, len(actions))
	for i, act := range actions {
		entries[i] = model.AuditEntry{Action: act}
		if e, ok := emails[act.UserID]; ok {
			entries[i].UserEmail = &e
		}
	}
	return writeList(w, entries)
}

// getPermissions is PermissionsResource.get (administrators): the first two query
// parameters name the owner and property keys, e.g. ?userId=1&deviceId=0.
func (a *API) getPermissions(w http.ResponseWriter, r *request) error {
	if err := checkAdmin(r); err != nil {
		return err
	}
	parts := strings.Split(r.URL.RawQuery, "&")
	if len(parts) < 2 {
		return errors.New("owner and property parameters required")
	}
	owner, ownerID, err := permissionParam(parts[0])
	if err != nil {
		return err
	}
	property, propertyID, err := permissionParam(parts[1])
	if err != nil {
		return err
	}
	rows, err := a.st.LinkRows(r.Context(), owner, ownerID, property, propertyID)
	if err != nil {
		return err
	}
	return writeList(w, rows)
}

func permissionParam(p string) (store.Kind, int64, error) {
	key, value, ok := strings.Cut(p, "=")
	kind, known := kindByKey[strings.ToLower(key)]
	if !ok || !known {
		return "", 0, errors.New("unknown permission key " + key)
	}
	id, err := parseInt64(value)
	if err != nil {
		return "", 0, err
	}
	return kind, id, nil
}

// kindByKey maps "<class>Id" keys (case-insensitive, as Java's class lookup) to kinds.
var kindByKey = func() map[string]store.Kind {
	m := map[string]store.Kind{}
	for _, k := range []store.Kind{store.KindUser, store.KindManagedUser, store.KindDevice, store.KindLinkedDevice,
		store.KindGroup, store.KindGeofence, store.KindNotification, store.KindDriver, store.KindAttribute,
		store.KindCalendar, store.KindCommand, store.KindMaintenance, store.KindOrder, store.KindReport} {
		m[strings.ToLower(k.Key())] = k
	}
	return m
}()

// getCommandTypes is CommandResource types: for a device, what its protocol (from its
// latest position) supports; without one, every command type.
func (a *API) getCommandTypes(w http.ResponseWriter, r *request) error {
	deviceID, err := qInt64(r, "deviceId")
	if err != nil {
		return err
	}
	if deviceID == 0 {
		return writeList(w, typed(model.CommandTypes))
	}
	if err := a.checkPermission(r, store.KindDevice, deviceID); err != nil {
		return err
	}
	device, err := store.Get[model.Device](r.Context(), a.st, deviceID)
	if err != nil {
		return err
	}
	if sender, ok := a.commandSender(&device); ok {
		return writeList(w, typed(sender))
	}
	proto, err := a.deviceProtocol(r, deviceID)
	if err != nil {
		return err
	}
	if proto == "" {
		return writeList(w, typed([]string{"custom"}))
	}
	return writeList(w, typed(protocol.SupportedCommands(proto, qBool(r, "textChannel"))))
}

// getSendableCommands is GET /api/commands/send: saved commands linked to both the user
// and the device that the device's protocol supports.
func (a *API) getSendableCommands(w http.ResponseWriter, r *request) error {
	deviceID, err := qInt64(r, "deviceId")
	if err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindDevice, deviceID); err != nil {
		return err
	}
	proto, err := a.deviceProtocol(r, deviceID)
	if err != nil {
		return err
	}
	commands, err := store.List[model.Command](r.Context(), a.st, store.ListOptions{Where: []store.Cond{
		store.Permission(store.KindUser, r.user.ID, store.KindCommand),
		store.Permission(store.KindDevice, deviceID, store.KindCommand),
	}})
	if err != nil {
		return err
	}
	commands = slices.DeleteFunc(commands, func(c model.Command) bool {
		if proto == "" {
			return c.Type != "custom"
		}
		return !slices.Contains(protocol.SupportedCommands(proto, c.TextChannel), c.Type)
	})
	return writeList(w, commands)
}

// deviceProtocol is the protocol of the device's latest position, if this server runs
// it (ServerManager.getProtocol only knows enabled protocols); "" otherwise.
func (a *API) deviceProtocol(r *request, deviceID int64) (string, error) {
	positions, err := a.st.LatestPositions(r.Context(), deviceID, 0)
	if err != nil || len(positions) == 0 || positions[0].Protocol == nil {
		return "", err
	}
	if a.opts.ProtocolEnabled != nil && !a.opts.ProtocolEnabled(*positions[0].Protocol) {
		return "", nil
	}
	return *positions[0].Protocol, nil
}

// commandSender is CommandSenderManager.getSender for the senders whose command lists
// are fixed: a device with command.sender set, or push tokens and a configured service.
func (a *API) commandSender(d *model.Device) ([]string, bool) {
	push := []string{"positionSingle", "positionPeriodic", "positionStop", "factoryReset"}
	if v, ok := d.Attributes.Get("command.sender"); ok {
		switch v {
		case "firebase", "traccar":
			return push, true
		}
		return nil, false
	}
	if _, ok := d.Attributes.Get("notificationTokens"); ok && a.opts.PushCommands {
		return push, true
	}
	return nil, false
}

func typed(types []string) []model.Typed {
	out := make([]model.Typed, len(types))
	for i, t := range types {
		out[i] = model.Typed{Type: t}
	}
	return out
}

func getNotificationTypes(w http.ResponseWriter, _ *request) error {
	return writeList(w, typed(model.EventTypes))
}

// getNotificators lists notificator.types; with ?announcement=true, without the ones that
// can't send announcements. Java returns a set whose order changes on every start.
func (a *API) getNotificators(w http.ResponseWriter, r *request) error {
	var out []string
	for _, t := range a.opts.Notificators {
		if qBool(r, "announcement") && (t == "command" || t == "web") {
			continue
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return writeList(w, typed(out))
}

func getTimezones(w http.ResponseWriter, _ *request) error {
	b := []byte{'['}
	for i, z := range timezones {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, z...)
		b = append(b, '"')
	}
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write(append(b, ']'))
	return err
}

// getGC is GET /api/server/gc (administrators): a garbage collection, empty 200.
func getGC(w http.ResponseWriter, r *request) error {
	if err := checkAdmin(r); err != nil {
		return err
	}
	runtime.GC()
	w.WriteHeader(http.StatusOK)
	return nil
}

// getHealth is HealthResource: "OK" when the database answers, else an empty 500.
func (a *API) getHealth(w http.ResponseWriter, r *request) error {
	if _, err := a.server(r); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", "text/plain")
	_, err := w.Write([]byte("OK"))
	return err
}
