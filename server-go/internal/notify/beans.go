package notify

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/velocity"
)

// bean is a Traccar model object as templates see it: its getters as properties (and as
// getX()/isX() methods), and ExtendedModel's attribute getters.
type bean struct {
	props map[string]any
	attrs *model.Attributes
}

func (b *bean) String() string { return "bean" } // Object.toString: never empty

func (b *bean) Property(name string) (any, bool) {
	if name == "attributes" && b.attrs != nil {
		return attributeMap(b.attrs), true
	}
	v, ok := b.props[name]
	return v, ok
}

func (b *bean) Method(name string, args []any) (any, bool, error) {
	if len(args) == 0 {
		for _, prefix := range []string{"get", "is"} {
			if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
				prop := strings.ToLower(rest[:1]) + rest[1:]
				if v, ok := b.Property(prop); ok {
					return v, true, nil
				}
			}
		}
		return nil, false, nil
	}
	if b.attrs == nil || len(args) != 1 {
		return nil, false, nil
	}
	key, ok := args[0].(string)
	if !ok {
		return nil, false, nil
	}
	a := b.attrs
	switch name {
	case "getString":
		if s := a.JavaString(key); s != nil {
			return *s, true, nil
		}
		return nil, true, nil
	case "hasAttribute":
		return a.Has(key), true, nil
	case "getBoolean":
		return a.JavaBool(key), true, nil
	case "getInteger":
		v, err := a.JavaInt(key)
		return v, true, err
	case "getLong":
		v, err := a.JavaLong(key)
		return v, true, err
	case "getDouble":
		v, err := a.JavaDouble(key, 0)
		return v, true, err
	}
	return nil, false, nil
}

// attributeMap is getAttributes(): the values as Velocity values.
func attributeMap(a *model.Attributes) map[string]any {
	m := map[string]any{}
	for k, v := range a.All() {
		m[k] = value(v)
	}
	return m
}

// value converts a model value to a Velocity value.
func value(v any) any {
	switch x := v.(type) {
	case model.Time:
		if x.IsZero() {
			return nil
		}
		return velocity.NewDate(x.Time)
	case *string:
		if x == nil {
			return nil
		}
		return *x
	case model.Float:
		return float64(x)
	}
	return v
}

func newBean(attrs *model.Attributes, props map[string]any) *bean {
	for k, v := range props {
		props[k] = value(v)
	}
	return &bean{props: props, attrs: attrs}
}

func deviceBean(d *model.Device) any {
	if d == nil {
		return nil
	}
	return newBean(&d.Attributes, map[string]any{
		"id": d.ID, "groupId": d.GroupID, "calendarId": d.CalendarID, "name": d.Name, "uniqueId": d.UniqueID,
		"phone": d.Phone, "model": d.Model, "contact": d.Contact, "category": d.Category, "disabled": d.Disabled,
		"expirationTime": d.ExpirationTime, "status": d.Status, "lastUpdate": d.LastUpdate, "positionId": d.PositionID,
		"motionStreak": d.MotionStreak, "motionState": d.MotionState, "motionPositionId": d.MotionPositionID,
		"motionTime": d.MotionTime, "motionDistance": d.MotionDistance, "motionLatitude": d.MotionLatitude,
		"motionLongitude": d.MotionLongitude, "overspeedState": d.OverspeedState, "overspeedTime": d.OverspeedTime,
		"overspeedGeofenceId": d.OverspeedGeofenceID,
	})
}

func eventBean(e *model.Event) any {
	if e == nil {
		return nil
	}
	return newBean(&e.Attributes, map[string]any{
		"id": e.ID, "deviceId": e.DeviceID, "type": e.Type, "eventTime": e.EventTime, "positionId": e.PositionID,
		"geofenceId": e.GeofenceID, "maintenanceId": e.MaintenanceID,
	})
}

func positionBean(p *model.Position) any {
	if p == nil {
		return nil
	}
	return newBean(&p.Attributes, map[string]any{
		"id": p.ID, "deviceId": p.DeviceID, "protocol": p.Protocol, "serverTime": p.ServerTime,
		"deviceTime": p.DeviceTime, "fixTime": p.FixTime, "outdated": p.Outdated, "valid": p.Valid,
		"latitude": p.Latitude, "longitude": p.Longitude, "altitude": p.Altitude, "speed": p.Speed,
		"course": p.Course, "address": p.Address, "accuracy": p.Accuracy,
	})
}

func geofenceBean(g *model.Geofence) any {
	if g == nil {
		return nil
	}
	return newBean(&g.Attributes, map[string]any{
		"id": g.ID, "calendarId": g.CalendarID, "name": g.Name, "description": g.Description, "area": g.Area,
	})
}

func maintenanceBean(m *model.Maintenance) any {
	if m == nil {
		return nil
	}
	return newBean(&m.Attributes, map[string]any{
		"id": m.ID, "name": m.Name, "type": m.Type, "start": m.Start, "period": m.Period,
	})
}

func driverBean(d *model.Driver) any {
	if d == nil {
		return nil
	}
	return newBean(&d.Attributes, map[string]any{"id": d.ID, "name": d.Name, "uniqueId": d.UniqueID})
}

func userBean(u *model.User) any {
	if u == nil {
		return nil
	}
	return newBean(&u.Attributes, map[string]any{
		"id": u.ID, "name": u.Name, "login": u.Login, "email": u.Email, "phone": u.Phone,
		"readonly": u.Readonly, "administrator": u.Administrator, "disabled": u.Disabled,
		"expirationTime": u.ExpirationTime, "deviceLimit": u.DeviceLimit, "userLimit": u.UserLimit,
	})
}

func notificationBean(n *model.Notification) any {
	if n == nil {
		return nil
	}
	return newBean(&n.Attributes, map[string]any{
		"id": n.ID, "description": n.Description, "calendarId": n.CalendarID, "always": n.Always,
		"type": n.Type, "commandId": n.CommandID, "notificators": n.Notificators,
	})
}
