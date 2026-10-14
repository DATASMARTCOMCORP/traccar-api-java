package store

import "github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"

// Column sets come from Java's ReflectionCache: for each model class, the getters
// without @QueryIgnore. They were extracted from the 6.14.5 jar, not written by hand.
// *Id columns go through id/scanID (0 ⇄ NULL), timestamps through s.ts, and JSON
// columns through the adapters in columns.go. Columns the schema allows to be NULL are
// scanned with nullBool/nullInt/nullFloat, reading NULL as false/0/0.0 like JDBC's
// getBoolean/getInt/getDouble; Java (and Go) then write the value back, never NULL.

var serverTable = &table[model.Server]{
	name: "tc_servers",
	columns: []string{"attributes", "registration", "readonly", "devicereadonly", "map", "bingkey",
		"mapurl", "overlayurl", "latitude", "longitude", "zoom", "forcesettings", "coordinateformat",
		"limitcommands", "disablereports", "fixedemail", "poilayer", "announcement"},
	values: func(s *Store, v *model.Server) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Registration, v.Readonly, v.DeviceReadonly, v.Map, v.BingKey,
			v.MapURL, v.OverlayURL, v.Latitude, v.Longitude, v.Zoom, v.ForceSettings, v.CoordinateFormat,
			v.LimitCommands, v.DisableReports, v.FixedEmail, v.PoiLayer, v.Announcement}, err
	},
	targets: func(s *Store, v *model.Server) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Registration, &v.Readonly, nullBool{&v.DeviceReadonly}, &v.Map, &v.BingKey,
			&v.MapURL, &v.OverlayURL, &v.Latitude, &v.Longitude, &v.Zoom, &v.ForceSettings, &v.CoordinateFormat,
			nullBool{&v.LimitCommands}, nullBool{&v.DisableReports}, nullBool{&v.FixedEmail}, &v.PoiLayer, &v.Announcement}
	},
	id: func(v *model.Server) *int64 { return &v.ID },
}

var userTable = &table[model.User]{
	name: "tc_users",
	columns: []string{"attributes", "name", "login", "email", "phone", "readonly", "administrator", "map",
		"latitude", "longitude", "zoom", "coordinateformat", "disabled", "expirationtime", "devicelimit",
		"userlimit", "devicereadonly", "limitcommands", "disablereports", "fixedemail", "poilayer", "totpkey",
		"temporary"},
	extra: []string{"hashedpassword", "salt"},
	values: func(s *Store, v *model.User) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Name, v.Login, v.Email, v.Phone, v.Readonly, v.Administrator, v.Map,
			v.Latitude, v.Longitude, v.Zoom, v.CoordinateFormat, v.Disabled, s.ts(&v.ExpirationTime), v.DeviceLimit,
			v.UserLimit, v.DeviceReadonly, v.LimitCommands, v.DisableReports, v.FixedEmail, v.PoiLayer, v.TotpKey,
			v.Temporary}, err
	},
	targets: func(s *Store, v *model.User) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Name, &v.Login, &v.Email, &v.Phone, &v.Readonly, nullBool{&v.Administrator}, &v.Map,
			&v.Latitude, &v.Longitude, &v.Zoom, &v.CoordinateFormat, nullBool{&v.Disabled}, s.ts(&v.ExpirationTime), nullInt{&v.DeviceLimit},
			nullInt{&v.UserLimit}, nullBool{&v.DeviceReadonly}, nullBool{&v.LimitCommands}, nullBool{&v.DisableReports}, nullBool{&v.FixedEmail}, &v.PoiLayer, &v.TotpKey,
			nullBool{&v.Temporary}, &v.HashedPassword, &v.Salt}
	},
	id: func(v *model.User) *int64 { return &v.ID },
}

var groupTable = &table[model.Group]{
	name:    "tc_groups",
	columns: []string{"attributes", "groupid", "name"},
	values: func(s *Store, v *model.Group) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.GroupID), v.Name}, err
	},
	targets: func(s *Store, v *model.Group) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.GroupID), &v.Name}
	},
	id: func(v *model.Group) *int64 { return &v.ID },
}

var deviceTable = &table[model.Device]{
	name: "tc_devices",
	columns: []string{"attributes", "groupid", "calendarid", "name", "uniqueid", "phone", "model", "contact",
		"category", "disabled", "expirationtime"},
	extra: []string{"status", "lastupdate", "positionid", "motionstreak", "motionstate", "motionpositionid",
		"motiontime", "motiondistance", "motionlatitude", "motionlongitude", "overspeedstate", "overspeedtime",
		"overspeedgeofenceid"},
	values: func(s *Store, v *model.Device) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.GroupID), id(v.CalendarID), v.Name, v.UniqueID, v.Phone, v.Model, v.Contact,
			v.Category, v.Disabled, s.ts(&v.ExpirationTime)}, err
	},
	targets: func(s *Store, v *model.Device) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.GroupID), scanID(&v.CalendarID), &v.Name, &v.UniqueID,
			&v.Phone, &v.Model, &v.Contact, &v.Category, nullBool{&v.Disabled}, s.ts(&v.ExpirationTime),
			status{&v.Status}, s.ts(&v.LastUpdate), scanID(&v.PositionID), nullBool{&v.MotionStreak}, nullBool{&v.MotionState},
			scanID(&v.MotionPositionID), s.ts(&v.MotionTime), nullFloat{&v.MotionDistance}, nullFloat{&v.MotionLatitude},
			nullFloat{&v.MotionLongitude}, nullBool{&v.OverspeedState}, s.ts(&v.OverspeedTime), scanID(&v.OverspeedGeofenceID)}
	},
	id: func(v *model.Device) *int64 { return &v.ID },
}

var positionTable = &table[model.Position]{
	name: "tc_positions",
	columns: []string{"attributes", "deviceid", "protocol", "servertime", "devicetime", "fixtime", "valid",
		"latitude", "longitude", "altitude", "speed", "course", "address", "accuracy", "network", "geofenceids"},
	values: func(s *Store, v *model.Position) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.DeviceID), v.Protocol, s.ts(&v.ServerTime), s.ts(&v.DeviceTime), s.ts(&v.FixTime), v.Valid,
			v.Latitude, v.Longitude, v.Altitude, v.Speed, v.Course, v.Address, v.Accuracy,
			network{&v.Network}.Value(), longList{&v.GeofenceIDs}.Value()}, err
	},
	targets: func(s *Store, v *model.Position) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.DeviceID), &v.Protocol, s.ts(&v.ServerTime), s.ts(&v.DeviceTime),
			s.ts(&v.FixTime), &v.Valid, &v.Latitude, &v.Longitude, &v.Altitude, &v.Speed, &v.Course, &v.Address, &v.Accuracy,
			network{&v.Network}, longList{&v.GeofenceIDs}}
	},
	id: func(v *model.Position) *int64 { return &v.ID },
}

var eventTable = &table[model.Event]{
	name:    "tc_events",
	columns: []string{"attributes", "deviceid", "type", "eventtime", "positionid", "geofenceid", "maintenanceid"},
	values: func(s *Store, v *model.Event) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.DeviceID), v.Type, s.ts(&v.EventTime), id(v.PositionID), id(v.GeofenceID), id(v.MaintenanceID)}, err
	},
	targets: func(s *Store, v *model.Event) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.DeviceID), &v.Type, s.ts(&v.EventTime), scanID(&v.PositionID),
			scanID(&v.GeofenceID), scanID(&v.MaintenanceID)}
	},
	id: func(v *model.Event) *int64 { return &v.ID },
}

var geofenceTable = &table[model.Geofence]{
	name:    "tc_geofences",
	columns: []string{"attributes", "calendarid", "name", "description", "area"},
	values: func(s *Store, v *model.Geofence) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.CalendarID), v.Name, v.Description, v.Area}, err
	},
	targets: func(s *Store, v *model.Geofence) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.CalendarID), &v.Name, &v.Description, &v.Area}
	},
	id: func(v *model.Geofence) *int64 { return &v.ID },
}

var driverTable = &table[model.Driver]{
	name:    "tc_drivers",
	columns: []string{"attributes", "name", "uniqueid"},
	values: func(s *Store, v *model.Driver) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Name, v.UniqueID}, err
	},
	targets: func(s *Store, v *model.Driver) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Name, &v.UniqueID}
	},
	id: func(v *model.Driver) *int64 { return &v.ID },
}

var computedAttributeTable = &table[model.ComputedAttribute]{
	name:    "tc_attributes",
	columns: []string{"description", "attribute", "expression", "type", "priority"},
	values: func(s *Store, v *model.ComputedAttribute) ([]any, error) {
		return []any{v.Description, v.Attribute, v.Expression, v.Type, v.Priority}, nil
	},
	targets: func(s *Store, v *model.ComputedAttribute) []any {
		return []any{&v.ID, &v.Description, &v.Attribute, &v.Expression, &v.Type, &v.Priority}
	},
	id: func(v *model.ComputedAttribute) *int64 { return &v.ID },
}

var calendarTable = &table[model.Calendar]{
	name:    "tc_calendars",
	columns: []string{"attributes", "name", "data"},
	values: func(s *Store, v *model.Calendar) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Name, v.Data}, err
	},
	targets: func(s *Store, v *model.Calendar) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Name, &v.Data}
	},
	id: func(v *model.Calendar) *int64 { return &v.ID },
}

var commandTable = &table[model.Command]{
	name:    "tc_commands",
	columns: []string{"attributes", "description", "type", "textchannel"},
	values: func(s *Store, v *model.Command) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Description, v.Type, v.TextChannel}, err
	},
	targets: func(s *Store, v *model.Command) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Description, &v.Type, &v.TextChannel}
	},
	id: func(v *model.Command) *int64 { return &v.ID },
}

var queuedCommandTable = &table[model.QueuedCommand]{
	name:    "tc_commands_queue",
	columns: []string{"attributes", "deviceid", "type", "textchannel"},
	values: func(s *Store, v *model.QueuedCommand) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.DeviceID), v.Type, v.TextChannel}, err
	},
	targets: func(s *Store, v *model.QueuedCommand) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.DeviceID), &v.Type, &v.TextChannel}
	},
	id: func(v *model.QueuedCommand) *int64 { return &v.ID },
}

var notificationTable = &table[model.Notification]{
	name:    "tc_notifications",
	columns: []string{"attributes", "description", "calendarid", "always", "type", "commandid", "notificators"},
	values: func(s *Store, v *model.Notification) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Description, id(v.CalendarID), v.Always, v.Type, id(v.CommandID), v.Notificators}, err
	},
	targets: func(s *Store, v *model.Notification) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Description, scanID(&v.CalendarID), &v.Always, &v.Type,
			scanID(&v.CommandID), &v.Notificators}
	},
	id: func(v *model.Notification) *int64 { return &v.ID },
}

var maintenanceTable = &table[model.Maintenance]{
	name:    "tc_maintenances",
	columns: []string{"attributes", "name", "type", "start", "period"},
	values: func(s *Store, v *model.Maintenance) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.Name, v.Type, v.Start, v.Period}, err
	},
	targets: func(s *Store, v *model.Maintenance) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.Name, &v.Type, &v.Start, &v.Period}
	},
	id: func(v *model.Maintenance) *int64 { return &v.ID },
}

var orderTable = &table[model.Order]{
	name:    "tc_orders",
	columns: []string{"attributes", "uniqueid", "description", "fromaddress", "toaddress"},
	values: func(s *Store, v *model.Order) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, v.UniqueID, v.Description, v.FromAddress, v.ToAddress}, err
	},
	targets: func(s *Store, v *model.Order) []any {
		return []any{&v.ID, attrs{&v.Attributes}, &v.UniqueID, &v.Description, &v.FromAddress, &v.ToAddress}
	},
	id: func(v *model.Order) *int64 { return &v.ID },
}

var reportTable = &table[model.Report]{
	name:    "tc_reports",
	columns: []string{"attributes", "calendarid", "type", "description"},
	values: func(s *Store, v *model.Report) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, id(v.CalendarID), v.Type, v.Description}, err
	},
	targets: func(s *Store, v *model.Report) []any {
		return []any{&v.ID, attrs{&v.Attributes}, scanID(&v.CalendarID), &v.Type, &v.Description}
	},
	id: func(v *model.Report) *int64 { return &v.ID },
}

var statisticsTable = &table[model.Statistics]{
	name: "tc_statistics",
	columns: []string{"attributes", "capturetime", "activeusers", "activedevices", "requests", "messagesreceived",
		"messagesstored", "mailsent", "smssent", "geocoderrequests", "geolocationrequests", "protocols"},
	values: func(s *Store, v *model.Statistics) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, s.ts(&v.CaptureTime), v.ActiveUsers, v.ActiveDevices, v.Requests, v.MessagesReceived,
			v.MessagesStored, v.MailSent, v.SmsSent, v.GeocoderRequests, v.GeolocationRequests,
			optionalAttrs{&v.Protocols}.Value()}, err
	},
	targets: func(s *Store, v *model.Statistics) []any {
		return []any{&v.ID, attrs{&v.Attributes}, s.ts(&v.CaptureTime), &v.ActiveUsers, &v.ActiveDevices, &v.Requests,
			&v.MessagesReceived, &v.MessagesStored, &v.MailSent, &v.SmsSent, &v.GeocoderRequests, &v.GeolocationRequests,
			optionalAttrs{&v.Protocols}}
	},
	id: func(v *model.Statistics) *int64 { return &v.ID },
}

var actionTable = &table[model.Action]{
	name:    "tc_actions",
	columns: []string{"attributes", "actiontime", "address", "userid", "actiontype", "objecttype", "objectid"},
	values: func(s *Store, v *model.Action) ([]any, error) {
		a, err := attrsValue(&v.Attributes)
		return []any{a, s.ts(&v.ActionTime), v.Address, id(v.UserID), v.ActionType, v.ObjectType, id(v.ObjectID)}, err
	},
	targets: func(s *Store, v *model.Action) []any {
		return []any{&v.ID, attrs{&v.Attributes}, s.ts(&v.ActionTime), &v.Address, scanID(&v.UserID), &v.ActionType,
			&v.ObjectType, scanID(&v.ObjectID)}
	},
	id: func(v *model.Action) *int64 { return &v.ID },
}
