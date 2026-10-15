package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Create, update and delete (MIGRATION.md Step 5), as Java's BaseObjectResource does them,
// with the special cases of the user, device, server and permission resources.

// writable describes how an entity type is written. groupID, calendarID and commandID are
// set for GroupedModel, Schedulable and Notification types (the checks in checkEdit).
type writable[T any] struct {
	kind       store.Kind
	bean       func(*T) bean
	id         func(*T) *int64
	groupID    func(*T) int64
	calendarID func(*T) int64
	commandID  func(*T) int64
}

func (a *API) writeRoutes() {
	registerWrites(a, "/api/devices", writable[model.Device]{kind: store.KindDevice, bean: deviceBean,
		id: func(d *model.Device) *int64 { return &d.ID }, groupID: func(d *model.Device) int64 { return d.GroupID },
		calendarID: func(d *model.Device) int64 { return d.CalendarID }})
	registerWrites(a, "/api/groups", writable[model.Group]{kind: store.KindGroup, bean: groupBean,
		id: func(g *model.Group) *int64 { return &g.ID }, groupID: func(g *model.Group) int64 { return g.GroupID }})
	registerWrites(a, "/api/geofences", writable[model.Geofence]{kind: store.KindGeofence, bean: geofenceBean,
		id: func(g *model.Geofence) *int64 { return &g.ID }, calendarID: func(g *model.Geofence) int64 { return g.CalendarID }})
	registerWrites(a, "/api/notifications", writable[model.Notification]{kind: store.KindNotification, bean: notificationBean,
		id: func(n *model.Notification) *int64 { return &n.ID }, calendarID: func(n *model.Notification) int64 { return n.CalendarID },
		commandID: func(n *model.Notification) int64 { return n.CommandID }})
	registerWrites(a, "/api/drivers", writable[model.Driver]{kind: store.KindDriver, bean: driverBean,
		id: func(d *model.Driver) *int64 { return &d.ID }})
	registerWrites(a, "/api/attributes/computed", writable[model.ComputedAttribute]{kind: store.KindAttribute,
		bean: computedAttributeBean, id: func(c *model.ComputedAttribute) *int64 { return &c.ID }})
	registerWrites(a, "/api/calendars", writable[model.Calendar]{kind: store.KindCalendar, bean: calendarBean,
		id: func(c *model.Calendar) *int64 { return &c.ID }})
	registerWrites(a, "/api/commands", writable[model.Command]{kind: store.KindCommand,
		bean: func(c *model.Command) bean {
			b := commandBean(c)
			b.notNull = []string{"description", "type"}
			return b
		}, id: func(c *model.Command) *int64 { return &c.ID }})
	registerWrites(a, "/api/maintenance", writable[model.Maintenance]{kind: store.KindMaintenance, bean: maintenanceBean,
		id: func(m *model.Maintenance) *int64 { return &m.ID }})
	registerWrites(a, "/api/orders", writable[model.Order]{kind: store.KindOrder, bean: orderBean,
		id: func(o *model.Order) *int64 { return &o.ID }})
	registerWrites(a, "/api/reports", writable[model.Report]{kind: store.KindReport, bean: reportBean,
		id: func(r *model.Report) *int64 { return &r.ID }, calendarID: func(r *model.Report) int64 { return r.CalendarID }})

	a.handle("POST /api/users", public, a.postUser)
	a.handle("PUT /api/users/{id}", authenticated, a.putUser)
	a.handle("DELETE /api/users/{id}", authenticated, a.deleteUser)
	a.handle("POST /api/users/totp", public, a.postTotp)
	a.handle("PUT /api/server", authenticated, a.putServer)
	a.handle("POST /api/permissions", authenticated, a.linkOne(true))
	a.handle("DELETE /api/permissions", authenticated, a.linkOne(false))
	a.handle("POST /api/permissions/bulk", authenticated, a.linkBulk(true))
	a.handle("DELETE /api/permissions/bulk", authenticated, a.linkBulk(false))
	a.handle("PUT /api/devices/{id}/accumulators", authenticated, a.putAccumulators)
	a.handle("POST /api/devices/{id}/image", authenticated, a.postDeviceImage)
	a.handle("DELETE /api/positions/{id}", authenticated, a.deletePosition)
	a.handle("DELETE /api/positions", authenticated, a.deletePositions)
	a.handle("POST /api/commands/send", authenticated, a.sendCommand)
	a.handle("POST /api/share/device", authenticated, a.share(store.KindDevice))
	a.handle("POST /api/share/group", authenticated, a.share(store.KindGroup))
	a.handle("POST /api/password/update", public, a.updatePassword)
}

func registerWrites[T interface {
	store.Entity
	jsonMarshaler
}](a *API, path string, w writable[T]) {
	a.handle("POST "+path, authenticated, func(rw http.ResponseWriter, r *request) error {
		var v T
		if err := a.readForInsert(r, w.bean(&v), string(w.kind)); err != nil {
			return err
		}
		if err := checkEdit(a, r, w, &v, true, false); err != nil {
			return err
		}
		if err := store.Insert(r.Context(), a.st, &v); err != nil {
			return err
		}
		id := *w.id(&v)
		a.audit(r.Context(), r.Request, "create", r.user.ID, string(w.kind), id)
		if r.user.ID != serviceAccountID {
			if err := a.st.AddLink(r.Context(), store.KindUser, r.user.ID, w.kind, id); err != nil {
				return err
			}
			a.auditLink(r, "link", store.KindUser, r.user.ID, w.kind, id)
		}
		return writeJSON(rw, http.StatusOK, v)
	})
	a.handle("PUT "+path+"/{id}", authenticated, func(rw http.ResponseWriter, r *request) error {
		// Java's update never reads the path parameter (so "abc" is fine there too).
		var v T
		if err := readJSON(r, w.bean(&v)); err != nil {
			return err
		}
		// Like Java, the id in the body is the one updated; the path id is not used.
		id := *w.id(&v)
		if err := a.checkPermission(r, w.kind, id); err != nil {
			return err
		}
		if w.kind == store.KindGroup {
			if err := a.checkGroupCycle(r, id, w.groupID(&v)); err != nil {
				return err
			}
		}
		if err := checkEdit(a, r, w, &v, false, false); err != nil {
			return err
		}
		if err := store.Update(r.Context(), a.st, &v); err != nil {
			return err
		}
		a.audit(r.Context(), r.Request, "edit", r.user.ID, string(w.kind), id)
		return writeJSON(rw, http.StatusOK, v)
	})
	a.handle("DELETE "+path+"/{id}", authenticated, func(rw http.ResponseWriter, r *request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := a.checkPermission(r, w.kind, id); err != nil {
			return err
		}
		if err := a.checkEditKind(r, w.kind, false, false); err != nil {
			return err
		}
		if err := store.Delete[T](r.Context(), a.st, id); err != nil {
			return err
		}
		a.audit(r.Context(), r.Request, "remove", r.user.ID, string(w.kind), id)
		rw.WriteHeader(http.StatusNoContent)
		return nil
	})
}

// readForInsert reads the body of a create. A NOT NULL property left null fails as Java's
// insert does: after using a sequence value of the table (kind's table).
func (a *API) readForInsert(r *request, b bean, kind string) error {
	err := readJSON(r, b)
	var missing *errNotNull
	if errors.As(err, &missing) {
		if cerr := a.st.ConsumeID(r.Context(), store.Kind(kind)); cerr != nil {
			return cerr
		}
	}
	return err
}

// checkEditKind is PermissionsService.checkEdit(userId, class, ...): read-only users and
// servers can't write; device writes also obey deviceReadonly and the device limit, and
// saved commands limitCommands.
func (a *API) checkEditKind(r *request, kind store.Kind, addition, skipReadonly bool) error {
	if r.user.Administrator {
		return nil
	}
	srv, err := a.server(r)
	if err != nil {
		return err
	}
	u := r.user
	denied := false
	switch {
	case !skipReadonly && (srv.Readonly || u.Readonly):
		denied = true
	case kind == store.KindDevice:
		denied = srv.DeviceReadonly || u.DeviceReadonly || addition && u.DeviceLimit == 0
		if !denied && addition && u.DeviceLimit > 0 {
			ids, err := store.IDs[model.Device](r.Context(), a.st, store.Permission(store.KindUser, u.ID, store.KindDevice))
			if err != nil {
				return err
			}
			denied = len(ids) >= u.DeviceLimit
		}
	case kind == store.KindCommand:
		denied = srv.LimitCommands || u.LimitCommands
	}
	if denied {
		return errors.New("Write access denied")
	}
	return nil
}

// checkEdit is PermissionsService.checkEdit(userId, object, ...): the kind check, plus
// permission on a group, calendar or command the object starts referring to.
func checkEdit[T store.Entity](a *API, r *request, w writable[T], v *T, addition, skipReadonly bool) error {
	if r.user.Administrator {
		return nil
	}
	if err := a.checkEditKind(r, w.kind, addition, skipReadonly); err != nil {
		return err
	}
	var before *T
	if !addition {
		b, err := store.Get[T](r.Context(), a.st, *w.id(v))
		switch {
		case err == nil:
			before = &b
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
	}
	refs := []struct {
		get  func(*T) int64
		kind store.Kind
	}{{w.groupID, store.KindGroup}, {w.calendarID, store.KindCalendar}, {w.commandID, store.KindCommand}}
	for _, ref := range refs {
		if ref.get == nil || ref.get(v) <= 0 {
			continue
		}
		if before == nil || ref.get(before) != ref.get(v) {
			if err := a.checkPermission(r, ref.kind, ref.get(v)); err != nil {
				return err
			}
		}
	}
	return nil
}

// maxGroupDepth is Storage.MAX_GROUP_DEPTH: how far up a group update looks for a cycle.
const maxGroupDepth = 3

func (a *API) checkGroupCycle(r *request, id, parentID int64) error {
	for depth := maxGroupDepth; parentID > 0 && depth > 0; depth-- {
		if parentID == id {
			return errors.New("Cycle in group hierarchy")
		}
		parent, err := store.Get[model.Group](r.Context(), a.st, parentID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			parentID = 0
		case err != nil:
			return err
		default:
			parentID = parent.GroupID
		}
	}
	return nil
}

// auditLink stores a link/unlink action: the property as the object, the owner in the
// attributes (LogAction.logLinkAction).
func (a *API) auditLink(r *request, action string, owner store.Kind, ownerID int64, property store.Kind, propertyID int64) {
	var attrs model.Attributes
	attrs.Set("ownerType", string(owner))
	attrs.Set("ownerId", ownerID)
	a.auditWith(r.Context(), r.Request, action, r.user.ID, string(property), propertyID, attrs)
}

func (a *API) auditWith(ctx context.Context, r *http.Request, actionType string, userID int64, objectType string, objectID int64, attrs model.Attributes) {
	act := model.Action{
		Attributes: attrs, ActionTime: model.NewTime(time.Now()), UserID: userID, ActionType: actionType, ObjectID: objectID,
	}
	addr := remoteAddress(r)
	act.Address = &addr
	if objectType != "" {
		act.ObjectType = &objectType
	}
	if err := store.Insert(ctx, a.st, &act); err != nil {
		slogWarn("failed to store action", actionType, err)
	}
}
