package model

import (
	"encoding/base64"
	"strconv"
)

// API JSON of entities, as Jackson writes the Java classes: every property is present
// (nulls included), doubles use Java's format (0.0, 1.0E7), dates use Time's format, and
// properties come in Java's order.

// object writes a JSON object property by property.
type object struct {
	b    []byte
	err  error
	used bool
}

func newObject() *object { return &object{b: []byte{'{'}} }

func (o *object) key(name string) {
	if o.used {
		o.b = append(o.b, ',')
	}
	o.used = true
	o.b = appendString(o.b, name)
	o.b = append(o.b, ':')
}

func (o *object) str(name, v string) { o.key(name); o.b = appendString(o.b, v) }

func (o *object) nullStr(name string, v *string) {
	o.key(name)
	if v == nil {
		o.b = append(o.b, "null"...)
		return
	}
	o.b = appendString(o.b, *v)
}

func (o *object) int(name string, v int64)      { o.key(name); o.b = strconv.AppendInt(o.b, v, 10) }
func (o *object) double(name string, v float64) { o.key(name); o.b = appendDouble(o.b, v) }
func (o *object) boolean(name string, v bool)   { o.key(name); o.b = strconv.AppendBool(o.b, v) }
func (o *object) time(name string, v Time)      { o.key(name); o.b = v.appendJSON(o.b) }
func (o *object) null(name string)              { o.key(name); o.b = append(o.b, "null"...) }
func (o *object) raw(name string, v []byte)     { o.key(name); o.b = append(o.b, v...) }

func (o *object) attributes(name string, a *Attributes) {
	o.key(name)
	if o.err != nil {
		return
	}
	o.b, o.err = a.appendJSON(o.b)
}

func (o *object) end() ([]byte, error) {
	if o.err != nil {
		return nil, o.err
	}
	return append(o.b, '}'), nil
}

// MarshalJSON writes the user as the API returns it. The password is always null; the
// hash and salt are never included.
func (u User) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", u.ID)
	o.attributes("attributes", &u.Attributes)
	o.str("name", u.Name)
	o.nullStr("login", u.Login)
	o.str("email", u.Email)
	o.nullStr("phone", u.Phone)
	o.boolean("readonly", u.Readonly)
	o.boolean("administrator", u.Administrator)
	o.nullStr("map", u.Map)
	o.double("latitude", u.Latitude)
	o.double("longitude", u.Longitude)
	o.int("zoom", int64(u.Zoom))
	o.nullStr("coordinateFormat", u.CoordinateFormat)
	o.boolean("disabled", u.Disabled)
	o.time("expirationTime", u.ExpirationTime)
	o.int("deviceLimit", int64(u.DeviceLimit))
	o.int("userLimit", int64(u.UserLimit))
	o.boolean("deviceReadonly", u.DeviceReadonly)
	o.boolean("limitCommands", u.LimitCommands)
	o.boolean("disableReports", u.DisableReports)
	o.boolean("fixedEmail", u.FixedEmail)
	o.nullStr("poiLayer", u.PoiLayer)
	o.nullStr("totpKey", u.TotpKey)
	o.boolean("temporary", u.Temporary)
	o.null("password")
	return o.end()
}

// ServerInfo is GET /api/server: the tc_servers row plus values the server computes for
// each request.
type ServerInfo struct {
	Server
	EmailEnabled    bool
	GeocoderEnabled bool
	TextEnabled     bool
	StorageSpace    []int64 // administrators only; nil is null
	NewServer       bool
	OpenIDEnabled   bool
	OpenIDForce     bool
	Version         string
}

func (s ServerInfo) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", s.ID)
	o.attributes("attributes", &s.Attributes)
	o.boolean("registration", s.Registration)
	o.boolean("readonly", s.Readonly)
	o.boolean("deviceReadonly", s.DeviceReadonly)
	o.nullStr("map", s.Map)
	o.nullStr("bingKey", s.BingKey)
	o.nullStr("mapUrl", s.MapURL)
	o.nullStr("overlayUrl", s.OverlayURL)
	o.double("latitude", s.Latitude)
	o.double("longitude", s.Longitude)
	o.int("zoom", int64(s.Zoom))
	o.boolean("forceSettings", s.ForceSettings)
	o.nullStr("coordinateFormat", s.CoordinateFormat)
	o.boolean("limitCommands", s.LimitCommands)
	o.boolean("disableReports", s.DisableReports)
	o.boolean("fixedEmail", s.FixedEmail)
	o.nullStr("poiLayer", s.PoiLayer)
	o.nullStr("announcement", s.Announcement)
	o.boolean("emailEnabled", s.EmailEnabled)
	o.boolean("geocoderEnabled", s.GeocoderEnabled)
	o.boolean("textEnabled", s.TextEnabled)
	if s.StorageSpace == nil {
		o.null("storageSpace")
	} else {
		b := []byte{'['}
		for i, v := range s.StorageSpace {
			if i > 0 {
				b = append(b, ',')
			}
			b = strconv.AppendInt(b, v, 10)
		}
		o.raw("storageSpace", append(b, ']'))
	}
	o.boolean("newServer", s.NewServer)
	o.boolean("openIdEnabled", s.OpenIDEnabled)
	o.boolean("openIdForce", s.OpenIDForce)
	o.str("version", s.Version)
	return o.end()
}

func (o *object) idList(name string, v []int64) {
	o.key(name)
	if v == nil {
		o.b = append(o.b, "null"...)
		return
	}
	o.b = append(o.b, '[')
	for i, id := range v {
		if i > 0 {
			o.b = append(o.b, ',')
		}
		o.b = strconv.AppendInt(o.b, id, 10)
	}
	o.b = append(o.b, ']')
}

func (o *object) bytes(name string, v []byte) {
	o.key(name)
	if v == nil {
		o.b = append(o.b, "null"...)
		return
	}
	// Jackson writes byte[] as standard base64.
	o.b = appendString(o.b, base64.StdEncoding.EncodeToString(v))
}

func (d Device) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", d.ID)
	o.attributes("attributes", &d.Attributes)
	o.int("groupId", d.GroupID)
	o.int("calendarId", d.CalendarID)
	o.str("name", d.Name)
	o.str("uniqueId", d.UniqueID)
	status := StatusOffline // Device.getStatus: null reads as offline
	if d.Status != nil {
		status = *d.Status
	}
	o.str("status", status)
	o.time("lastUpdate", d.LastUpdate)
	o.int("positionId", d.PositionID)
	o.nullStr("phone", d.Phone)
	o.nullStr("model", d.Model)
	o.nullStr("contact", d.Contact)
	o.nullStr("category", d.Category)
	o.boolean("disabled", d.Disabled)
	o.time("expirationTime", d.ExpirationTime)
	return o.end()
}

func (g Group) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", g.ID)
	o.attributes("attributes", &g.Attributes)
	o.int("groupId", g.GroupID)
	o.str("name", g.Name)
	return o.end()
}

func (g Geofence) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", g.ID)
	o.attributes("attributes", &g.Attributes)
	o.int("calendarId", g.CalendarID)
	o.str("name", g.Name)
	o.nullStr("description", g.Description)
	o.str("area", g.Area)
	return o.end()
}

func (n Notification) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", n.ID)
	o.attributes("attributes", &n.Attributes)
	o.nullStr("description", n.Description)
	o.int("calendarId", n.CalendarID)
	o.boolean("always", n.Always)
	o.str("type", n.Type)
	o.int("commandId", n.CommandID)
	o.nullStr("notificators", n.Notificators)
	return o.end()
}

func (d Driver) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", d.ID)
	o.attributes("attributes", &d.Attributes)
	o.str("name", d.Name)
	o.str("uniqueId", d.UniqueID)
	return o.end()
}

func (a ComputedAttribute) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", a.ID)
	o.str("description", a.Description)
	o.str("attribute", a.Attribute)
	o.str("expression", a.Expression)
	o.str("type", a.Type)
	o.int("priority", int64(a.Priority))
	return o.end()
}

func (c Calendar) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", c.ID)
	o.attributes("attributes", &c.Attributes)
	o.str("name", c.Name)
	o.bytes("data", c.Data)
	return o.end()
}

func (c Command) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", c.ID)
	o.attributes("attributes", &c.Attributes)
	o.int("deviceId", c.DeviceID)
	o.str("type", c.Type)
	o.boolean("textChannel", c.TextChannel)
	o.nullStr("description", c.Description)
	return o.end()
}

func (q QueuedCommand) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", q.ID)
	o.attributes("attributes", &q.Attributes)
	o.int("deviceId", q.DeviceID)
	o.str("type", q.Type)
	o.boolean("textChannel", q.TextChannel)
	return o.end()
}

func (m Maintenance) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", m.ID)
	o.attributes("attributes", &m.Attributes)
	o.str("name", m.Name)
	o.str("type", m.Type)
	o.double("start", m.Start)
	o.double("period", m.Period)
	return o.end()
}

func (r Order) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", r.ID)
	o.attributes("attributes", &r.Attributes)
	o.str("uniqueId", r.UniqueID)
	o.nullStr("description", r.Description)
	o.nullStr("fromAddress", r.FromAddress)
	o.nullStr("toAddress", r.ToAddress)
	return o.end()
}

func (r Report) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", r.ID)
	o.attributes("attributes", &r.Attributes)
	o.int("calendarId", r.CalendarID)
	o.str("type", r.Type)
	o.str("description", r.Description)
	return o.end()
}

func (p Position) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", p.ID)
	o.attributes("attributes", &p.Attributes)
	o.int("deviceId", p.DeviceID)
	o.nullStr("protocol", p.Protocol)
	o.time("serverTime", p.ServerTime)
	o.time("deviceTime", p.DeviceTime)
	o.time("fixTime", p.FixTime)
	o.boolean("valid", p.Valid)
	o.double("latitude", p.Latitude)
	o.double("longitude", p.Longitude)
	o.double("altitude", p.Altitude)
	o.double("speed", p.Speed)
	o.double("course", p.Course)
	o.nullStr("address", p.Address)
	o.double("accuracy", p.Accuracy)
	o.key("network")
	if p.Network == nil {
		o.b = append(o.b, "null"...)
	} else {
		o.b = p.Network.appendJSON(o.b)
	}
	o.idList("geofenceIds", p.GeofenceIDs)
	return o.end()
}

func (e Event) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", e.ID)
	o.attributes("attributes", &e.Attributes)
	o.int("deviceId", e.DeviceID)
	o.str("type", e.Type)
	o.time("eventTime", e.EventTime)
	o.int("positionId", e.PositionID)
	o.int("geofenceId", e.GeofenceID)
	o.int("maintenanceId", e.MaintenanceID)
	return o.end()
}

func (s Statistics) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", s.ID)
	o.attributes("attributes", &s.Attributes)
	o.time("captureTime", s.CaptureTime)
	o.int("activeUsers", int64(s.ActiveUsers))
	o.int("activeDevices", int64(s.ActiveDevices))
	o.int("requests", int64(s.Requests))
	o.int("messagesReceived", int64(s.MessagesReceived))
	o.int("messagesStored", int64(s.MessagesStored))
	o.int("mailSent", int64(s.MailSent))
	o.int("smsSent", int64(s.SmsSent))
	o.int("geocoderRequests", int64(s.GeocoderRequests))
	o.int("geolocationRequests", int64(s.GeolocationRequests))
	if s.Protocols == nil {
		o.null("protocols")
	} else {
		o.attributes("protocols", s.Protocols)
	}
	return o.end()
}

// AuditEntry is an Action as GET /api/audit returns it, with the user's e-mail.
type AuditEntry struct {
	Action
	UserEmail *string
}

func (a AuditEntry) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("id", a.ID)
	o.attributes("attributes", &a.Attributes)
	o.time("actionTime", a.ActionTime)
	o.nullStr("address", a.Address)
	o.int("userId", a.UserID)
	o.str("actionType", a.ActionType)
	o.nullStr("objectType", a.ObjectType)
	o.int("objectId", a.ObjectID)
	o.nullStr("userEmail", a.UserEmail)
	return o.end()
}

// Typed is {"type": ...}, used by the type and notificator lists.
type Typed struct{ Type string }

func (t Typed) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.str("type", t.Type)
	return o.end()
}

// Permission is one link-table row as the API returns it: {"<owner>Id": n, "<property>Id": m}.
type Permission struct {
	OwnerKey    string
	OwnerID     int64
	PropertyKey string
	PropertyID  int64
}

func (p Permission) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int(p.OwnerKey, p.OwnerID)
	o.int(p.PropertyKey, p.PropertyID)
	return o.end()
}
