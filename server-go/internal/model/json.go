package model

import "strconv"

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
