package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/auth"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Property tables of the Java model classes: which JSON properties each accepts and what
// its setter does (trimming, hashing, validation). See decode.go for the rules.

func trim(s string) string { return strings.TrimSpace(s) }

// trimmed is a nullable String whose setter trims (setPhone, setStatus).
func trimmed(dst **string) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if err := stringProp(dst)(raw); err != nil {
			return err
		}
		if *dst != nil {
			t := strings.TrimSpace(**dst)
			*dst = &t
		}
		return nil
	}
}

func deviceBean(d *model.Device) bean {
	return bean{
		props: props{
			"id": longProp(&d.ID), "attributes": attributesProp(&d.Attributes),
			"groupId": longProp(&d.GroupID), "calendarId": longProp(&d.CalendarID),
			"name": plainString(&d.Name), "uniqueId": requiredString(&d.UniqueID, trim),
			"status": trimmed(&d.Status), "lastUpdate": timeProp(&d.LastUpdate), "positionId": longProp(&d.PositionID),
			"phone": trimmed(&d.Phone), "model": stringProp(&d.Model), "contact": stringProp(&d.Contact),
			"category": stringProp(&d.Category), "disabled": boolProp(&d.Disabled),
			"expirationTime": timeProp(&d.ExpirationTime),
		},
		ignored: []string{"motionStreak", "motionState", "motionPositionId", "motionTime", "motionDistance",
			"motionLatitude", "motionLongitude", "overspeedState", "overspeedTime", "overspeedGeofenceId"},
		notNull: []string{"name", "uniqueId"},
	}
}

// userBean reads a User. "password" hashes into HashedPassword/Salt like setPassword (an
// empty password changes nothing); the hash and salt themselves can't be set.
func userBean(u *model.User) bean {
	return bean{
		props: props{
			"id": longProp(&u.ID), "attributes": attributesProp(&u.Attributes),
			"name": plainString(&u.Name), "login": stringProp(&u.Login), "email": requiredString(&u.Email, trim),
			"phone": trimmed(&u.Phone), "readonly": boolProp(&u.Readonly), "administrator": boolProp(&u.Administrator),
			"map": stringProp(&u.Map), "latitude": doubleProp(&u.Latitude), "longitude": doubleProp(&u.Longitude),
			"zoom": intProp(&u.Zoom), "coordinateFormat": stringProp(&u.CoordinateFormat), "disabled": boolProp(&u.Disabled),
			"expirationTime": timeProp(&u.ExpirationTime), "deviceLimit": intProp(&u.DeviceLimit),
			"userLimit": intProp(&u.UserLimit), "deviceReadonly": boolProp(&u.DeviceReadonly),
			"limitCommands": boolProp(&u.LimitCommands), "disableReports": boolProp(&u.DisableReports),
			"fixedEmail": boolProp(&u.FixedEmail), "poiLayer": stringProp(&u.PoiLayer), "totpKey": stringProp(&u.TotpKey),
			"temporary": boolProp(&u.Temporary),
			"password": func(raw json.RawMessage) error {
				var p *string
				if err := stringProp(&p)(raw); err != nil || p == nil || *p == "" {
					return err
				}
				hash, salt, err := auth.HashPassword(*p)
				if err != nil {
					return err
				}
				u.HashedPassword, u.Salt = &hash, &salt
				return nil
			},
		},
		ignored: []string{"manager", "hashedPassword", "salt"},
		notNull: []string{"name", "email"},
	}
}

// serverBean reads a Server; it ignores unknown properties, and the computed ones are
// set on the response object only (they aren't stored).
func serverBean(s *model.ServerInfo) bean {
	return bean{
		ignoreUnknown: true,
		props: props{
			"id": longProp(&s.ID), "attributes": attributesProp(&s.Attributes),
			"registration": boolProp(&s.Registration), "readonly": boolProp(&s.Readonly),
			"deviceReadonly": boolProp(&s.DeviceReadonly), "map": stringProp(&s.Map), "bingKey": stringProp(&s.BingKey),
			"mapUrl": stringProp(&s.MapURL), "overlayUrl": stringProp(&s.OverlayURL),
			"latitude": doubleProp(&s.Latitude), "longitude": doubleProp(&s.Longitude), "zoom": intProp(&s.Zoom),
			"forceSettings": boolProp(&s.ForceSettings), "coordinateFormat": stringProp(&s.CoordinateFormat),
			"limitCommands": boolProp(&s.LimitCommands), "disableReports": boolProp(&s.DisableReports),
			"fixedEmail": boolProp(&s.FixedEmail), "poiLayer": stringProp(&s.PoiLayer),
			"announcement": stringProp(&s.Announcement),
			"emailEnabled": boolProp(&s.EmailEnabled), "textEnabled": boolProp(&s.TextEnabled),
			"geocoderEnabled": boolProp(&s.GeocoderEnabled), "newServer": boolProp(&s.NewServer),
			"openIdEnabled": boolProp(&s.OpenIDEnabled), "openIdForce": boolProp(&s.OpenIDForce),
			"storageSpace": func(raw json.RawMessage) error {
				if isNull(raw) {
					s.StorageSpace = nil
					return nil
				}
				return json.Unmarshal(raw, &s.StorageSpace)
			},
		},
	}
}

func groupBean(g *model.Group) bean {
	return bean{props: props{
		"id": longProp(&g.ID), "attributes": attributesProp(&g.Attributes),
		"groupId": longProp(&g.GroupID), "name": plainString(&g.Name),
	}, notNull: []string{"name"}}
}

func geofenceBean(g *model.Geofence) bean {
	return bean{props: props{
		"id": longProp(&g.ID), "attributes": attributesProp(&g.Attributes), "calendarId": longProp(&g.CalendarID),
		"name": plainString(&g.Name), "description": stringProp(&g.Description), "area": plainString(&g.Area),
	}, ignored: []string{"geometry"}, notNull: []string{"name", "area"}}
}

func notificationBean(n *model.Notification) bean {
	return bean{props: props{
		"id": longProp(&n.ID), "attributes": attributesProp(&n.Attributes), "description": stringProp(&n.Description),
		"calendarId": longProp(&n.CalendarID), "always": boolProp(&n.Always), "type": plainString(&n.Type),
		"commandId": longProp(&n.CommandID), "notificators": stringProp(&n.Notificators),
	}, ignored: []string{"notificatorsTypes"}, notNull: []string{"type"}}
}

func driverBean(d *model.Driver) bean {
	return bean{props: props{
		"id": longProp(&d.ID), "attributes": attributesProp(&d.Attributes),
		"name": plainString(&d.Name), "uniqueId": plainString(&d.UniqueID),
	}, notNull: []string{"name", "uniqueId"}}
}

func computedAttributeBean(c *model.ComputedAttribute) bean {
	return bean{props: props{
		"id": longProp(&c.ID), "description": plainString(&c.Description), "attribute": plainString(&c.Attribute),
		"expression": plainString(&c.Expression), "type": plainString(&c.Type), "priority": intProp(&c.Priority),
	}, notNull: []string{"description", "attribute", "expression", "type"}}
}

// calendarBean reads a Calendar; "data" is base64 iCalendar and setData parses it, so
// data that isn't a calendar fails.
func calendarBean(c *model.Calendar) bean {
	return bean{props: props{
		"id": longProp(&c.ID), "attributes": attributesProp(&c.Attributes), "name": plainString(&c.Name),
		"data": func(raw json.RawMessage) error {
			if isNull(raw) {
				c.Data = nil
				return nil
			}
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return err
			}
			data, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return err
			}
			text := string(data)
			if !strings.Contains(text, "BEGIN:VCALENDAR") || !strings.Contains(text, "END:VCALENDAR") {
				return errors.New("not an iCalendar")
			}
			c.Data = data
			return nil
		},
	}, ignored: []string{"calendar"}, notNull: []string{"name", "data"}}
}

// commandBean reads a saved command or a command to send (deviceId is only used to send).
func commandBean(c *model.Command) bean {
	return bean{ignoreUnknown: true, props: props{
		"id": longProp(&c.ID), "attributes": attributesProp(&c.Attributes), "deviceId": longProp(&c.DeviceID),
		"type": plainString(&c.Type), "textChannel": boolProp(&c.TextChannel), "description": plainString(&c.Description),
	}}
}

func maintenanceBean(m *model.Maintenance) bean {
	return bean{props: props{
		"id": longProp(&m.ID), "attributes": attributesProp(&m.Attributes), "name": plainString(&m.Name),
		"type": plainString(&m.Type), "start": doubleProp(&m.Start), "period": doubleProp(&m.Period),
	}, notNull: []string{"name", "type"}}
}

func orderBean(o *model.Order) bean {
	return bean{props: props{
		"id": longProp(&o.ID), "attributes": attributesProp(&o.Attributes), "uniqueId": plainString(&o.UniqueID),
		"description": stringProp(&o.Description), "fromAddress": stringProp(&o.FromAddress),
		"toAddress": stringProp(&o.ToAddress),
	}, notNull: []string{"uniqueId"}}
}

func reportBean(r *model.Report) bean {
	return bean{props: props{
		"id": longProp(&r.ID), "attributes": attributesProp(&r.Attributes), "calendarId": longProp(&r.CalendarID),
		"type": plainString(&r.Type), "description": plainString(&r.Description),
	}, notNull: []string{"type", "description"}}
}
