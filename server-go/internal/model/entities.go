// Package model holds the server's data types and the encodings they share with the Java
// server: dates (Time), attribute maps that keep Java's number kinds (Attributes), the
// position Network, and Jackson-compatible JSON text.
//
// Entities are plain structs, one per table. Nullable text columns are *string. Ids
// are int64, and 0 means "none": internal/store writes 0 as NULL for *Id columns and reads
// NULL back as 0, as Java does. Field comments name the column when it isn't obvious.
// The API's JSON encoding of entities is added with the read-only API (Step 4), where
// the harness can check it against Java.
package model

// Server is the single row of tc_servers.
type Server struct {
	ID               int64
	Attributes       Attributes
	Registration     bool
	Readonly         bool
	DeviceReadonly   bool
	Map              *string
	BingKey          *string
	MapURL           *string
	OverlayURL       *string
	Latitude         float64
	Longitude        float64
	Zoom             int
	ForceSettings    bool
	CoordinateFormat *string
	LimitCommands    bool
	DisableReports   bool
	FixedEmail       bool
	PoiLayer         *string
	Announcement     *string
}

// User is a row of tc_users. HashedPassword and Salt are only written by password
// changes, never by ordinary updates (as in Java).
type User struct {
	ID               int64
	Attributes       Attributes
	Name             string
	Login            *string
	Email            string
	Phone            *string
	Readonly         bool
	Administrator    bool
	Map              *string
	Latitude         float64
	Longitude        float64
	Zoom             int
	CoordinateFormat *string
	Disabled         bool
	ExpirationTime   Time
	DeviceLimit      int
	UserLimit        int
	DeviceReadonly   bool
	LimitCommands    bool
	DisableReports   bool
	FixedEmail       bool
	PoiLayer         *string
	TotpKey          *string
	Temporary        bool
	HashedPassword   *string
	Salt             *string
}

// Manager reports whether the user can manage other users, as User.getManager does.
func (u *User) Manager() bool { return u.UserLimit != 0 }

type Group struct {
	ID         int64
	Attributes Attributes
	GroupID    int64
	Name       string
}

// Device is a row of tc_devices. The fields after Expiration are device state: Java
// writes each group with its own targeted UPDATE (status, position, motion, overspeed),
// never through an ordinary device update.
type Device struct {
	ID             int64
	Attributes     Attributes
	GroupID        int64
	CalendarID     int64
	Name           string
	UniqueID       string
	Phone          *string
	Model          *string
	Contact        *string
	Category       *string
	Disabled       bool
	ExpirationTime Time

	Status     *string // stored blank-padded (character column); nil reads as "offline" in the API
	LastUpdate Time
	PositionID int64

	MotionStreak     bool
	MotionState      bool
	MotionPositionID int64
	MotionTime       Time
	MotionDistance   float64
	MotionLatitude   float64
	MotionLongitude  float64

	OverspeedState      bool
	OverspeedTime       Time
	OverspeedGeofenceID int64
}

// Device statuses.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
	StatusUnknown = "unknown"
)

// Position is a row of tc_positions.
type Position struct {
	ID         int64
	Attributes Attributes
	DeviceID   int64
	Protocol   *string
	ServerTime Time
	DeviceTime Time
	FixTime    Time
	Valid      bool
	Latitude   float64
	Longitude  float64
	Altitude   float64 // meters
	Speed      float64 // knots
	Course     float64
	Address    *string
	Accuracy   float64
	Network    *Network // nil is stored as the text "null", as Java does
	// GeofenceIDs is nil when unset (stored as "null"); an empty list is stored as "[]".
	GeofenceIDs []int64
}

type Event struct {
	ID            int64
	Attributes    Attributes
	DeviceID      int64
	Type          string
	EventTime     Time
	PositionID    int64
	GeofenceID    int64
	MaintenanceID int64
}

type Geofence struct {
	ID          int64
	Attributes  Attributes
	CalendarID  int64
	Name        string
	Description *string
	Area        string // WKT
}

type Driver struct {
	ID         int64
	Attributes Attributes
	Name       string
	UniqueID   string
}

// ComputedAttribute is a row of tc_attributes (Java's Attribute class).
type ComputedAttribute struct {
	ID          int64
	Description string
	Attribute   string
	Expression  string
	Type        string
	Priority    int
}

type Calendar struct {
	ID         int64
	Attributes Attributes
	Name       string
	Data       []byte // iCalendar
}

// Command is a saved command (tc_commands).
type Command struct {
	ID          int64
	Attributes  Attributes
	Description string
	Type        string
	TextChannel bool
}

// QueuedCommand waits in tc_commands_queue for its device to connect.
type QueuedCommand struct {
	ID          int64
	Attributes  Attributes
	DeviceID    int64
	Type        string
	TextChannel bool
}

type Notification struct {
	ID           int64
	Attributes   Attributes
	Description  *string
	CalendarID   int64
	Always       bool
	Type         string
	CommandID    int64
	Notificators *string // comma-separated
}

type Maintenance struct {
	ID         int64
	Attributes Attributes
	Name       string
	Type       string
	Start      float64
	Period     float64
}

type Order struct {
	ID          int64
	Attributes  Attributes
	UniqueID    string
	Description *string
	FromAddress *string
	ToAddress   *string
}

// Report is a scheduled report (tc_reports).
type Report struct {
	ID          int64
	Attributes  Attributes
	CalendarID  int64
	Type        string
	Description string
}

type Statistics struct {
	ID                  int64
	Attributes          Attributes
	CaptureTime         Time
	ActiveUsers         int
	ActiveDevices       int
	Requests            int
	MessagesReceived    int
	MessagesStored      int
	MailSent            int
	SmsSent             int
	GeocoderRequests    int
	GeolocationRequests int
	// Protocols counts messages per protocol. Java keeps a HashMap; the stored key order
	// is preserved on read. nil is stored as "null".
	Protocols *Attributes
}

// Action is an audit log row (tc_actions).
type Action struct {
	ID         int64
	Attributes Attributes
	ActionTime Time
	Address    *string
	UserID     int64
	ActionType string
	ObjectType *string
	ObjectID   int64
}
