package ingest

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Environment is what a decoder's surroundings provide: BaseProtocolDecoder's services
// (sessions, the command queue, the device cache) and the channel to reply on. A
// connection is one; tests use one that behaves like Traccar's BaseTest mocks.
type Environment interface {
	Protocol() string
	Config() *config.Config
	// DeviceSession is ConnectionManager.getDeviceSession for the sender.
	DeviceSession(ctx context.Context, remote string, uniqueIDs []string) (*DeviceSession, error)
	// QueuedCommands is CommandsManager.readQueuedCommands.
	QueuedCommands(ctx context.Context, deviceID int64, limit int) ([]model.QueuedCommand, error)
	// UpdateNotificationToken is CommandsManager.updateNotificationToken.
	UpdateNotificationToken(ctx context.Context, deviceID int64, token string) error
	// Device is the cached device (CacheManager.getObject(Device)); nil if unknown.
	Device(ctx context.Context, deviceID int64) (*model.Device, error)
	// Lookup is AttributeUtil.lookup before conversion: the device's, its groups', the
	// server's (when server) and the config's (when cfg) value of key, or nil.
	Lookup(ctx context.Context, deviceID int64, name string, server, cfg bool) (any, error)
	// Send is channel.writeAndFlush(new NetworkMessage(data, remote)): data is a *kit.Buf,
	// []byte or string. With no channel (tests), it does nothing.
	Send(remote string, data any)
	// LocalAddress is the channel's local "host:port", or "" without a channel.
	LocalAddress() string
	// WriteMedia is MediaManager.writeFile: it stores data as a file of the device and
	// returns the file name, or "" (Java's null) when media isn't stored.
	WriteMedia(uniqueID, extension string, data []byte) string
	// Zone is the server's time zone (the JVM's default zone).
	Zone() *time.Location
}

// Message is the context of one received message, what BaseProtocolDecoder gives a Java
// decoder.
type Message struct {
	env    Environment
	ctx    context.Context
	remote string // host:port of the sender
	Now    time.Time
}

// NewMessage returns a message from remote in env, received at now.
func NewMessage(ctx context.Context, env Environment, remote string, now time.Time) *Message {
	return &Message{env: env, ctx: ctx, remote: remote, Now: now}
}

// Context is the context of the message's processing.
func (m *Message) Context() context.Context { return m.ctx }

// Remote is the sender's "host:port".
func (m *Message) Remote() string { return m.remote }

// Protocol is the protocol name.
func (m *Message) Protocol() string { return m.env.Protocol() }

// Config is the server configuration.
func (m *Message) Config() *config.Config { return m.env.Config() }

// NewPosition is new Position(protocol), stamped with the server time.
func (m *Message) NewPosition() *model.Position {
	protocol := m.env.Protocol()
	return &model.Position{Protocol: &protocol, ServerTime: model.NewTime(m.Now)}
}

// DeviceSession is BaseProtocolDecoder.getDeviceSession. Nil means unknown; an error means
// the Java decoder would throw (a disabled device, a database failure).
func (m *Message) DeviceSession(uniqueIDs ...string) (*DeviceSession, error) {
	return m.env.DeviceSession(m.ctx, m.remote, uniqueIDs)
}

// LastLocation is BaseProtocolDecoder.getLastLocation: a position without a fix of its
// own, completed from the last position during processing.
func (m *Message) LastLocation(pos *model.Position, deviceTime *time.Time) {
	if pos.DeviceID != 0 {
		pos.Outdated = true
		if deviceTime != nil {
			pos.DeviceTime = model.NewTime(*deviceTime)
		}
	}
}

// ConvertSpeed is BaseProtocolDecoder.convertSpeed: <protocol>.speed overrides the units
// the protocol reports, and the result is in knots.
func (m *Message) ConvertSpeed(value float64, defaultUnits string) float64 {
	switch m.Config().String(m.Protocol()+".speed", defaultUnits) {
	case "kmh":
		return units.KnotsFromKph(value)
	case "mps":
		return units.KnotsFromMps(value)
	case "mph":
		return units.KnotsFromMph(value)
	}
	return value
}

// QueuedCommands is CommandsManager.readQueuedCommands.
func (m *Message) QueuedCommands(deviceID int64, limit int) ([]model.QueuedCommand, error) {
	return m.env.QueuedCommands(m.ctx, deviceID, limit)
}

// UpdateNotificationToken is CommandsManager.updateNotificationToken.
func (m *Message) UpdateNotificationToken(deviceID int64, token string) error {
	return m.env.UpdateNotificationToken(m.ctx, deviceID, token)
}

// Send replies to the sender (channel.writeAndFlush).
func (m *Message) Send(data any) { m.env.Send(m.remote, data) }

// SendTo writes to another address of the channel (UDP).
func (m *Message) SendTo(remote string, data any) { m.env.Send(remote, data) }

// HasChannel reports whether replies go anywhere (Java's "channel != null").
func (m *Message) HasChannel() bool { return m.env.LocalAddress() != "" }

// Device is cacheManager.getObject(Device.class, id).
func (m *Message) Device(deviceID int64) (*model.Device, error) { return m.env.Device(m.ctx, deviceID) }

// DeviceModel is getDeviceModel(deviceSession): the session's model ("" for null).
func (m *Message) DeviceModel(ds *DeviceSession) string {
	if ds.Model == nil {
		return ""
	}
	return *ds.Model
}

// LookupString is AttributeUtil.lookup for a String key.
func (m *Message) LookupString(deviceID int64, name string, server, cfg bool) (*string, error) {
	v, err := m.env.Lookup(m.ctx, deviceID, name, server, cfg)
	if err != nil || v == nil {
		return nil, err
	}
	s, ok := v.(string)
	if !ok {
		return nil, errJava
	}
	return &s, nil
}

// TimeZone is getTimeZone(deviceId, defaultTimeZone): decoder.timezone for the device, or
// the default ("UTC" when def is ""). Java's TimeZone.getTimeZone falls back to GMT for an
// unknown id.
func (m *Message) TimeZone(deviceID int64, def string) (*time.Location, error) {
	name, err := m.LookupString(deviceID, "decoder.timezone", false, true)
	if err != nil {
		return nil, err
	}
	id := def
	if id == "" {
		id = "UTC"
	}
	if name != nil {
		id = *name
	}
	return javatime.TimeZone(id), nil
}

// Server is getServer(channel, delimiter): <protocol>.server, or the channel's local
// address, with ':' replaced by the delimiter; "" without either.
func (m *Message) Server(delimiter byte) string {
	s, ok := m.Config().Lookup(m.Protocol() + ".server")
	if !ok {
		s = m.env.LocalAddress()
	}
	out := []byte(s)
	for i := range out {
		if out[i] == ':' {
			out[i] = delimiter
		}
	}
	return string(out)
}

// Decoder-side helpers. Protocol decoders fail by panicking (see kit.Fault), so these panic
// with the error where Java throws.

// Session is DeviceSession for decoders: nil for an unknown device; a failure panics.
func (m *Message) Session(uniqueIDs ...string) *DeviceSession {
	ds, err := m.DeviceSession(uniqueIDs...)
	if err != nil {
		panic(err)
	}
	return ds
}

func (m *Message) lookup(deviceID int64, name string, server, cfg bool) any {
	v, err := m.env.Lookup(m.ctx, deviceID, name, server, cfg)
	if err != nil {
		panic(err)
	}
	return v
}

// LookupInt is AttributeUtil.lookup for an Integer key: nil when unset; a string that
// isn't a number panics (NumberFormatException).
func (m *Message) LookupInt(deviceID int64, name string, server, cfg bool) *int {
	var r int
	switch v := m.lookup(deviceID, name, server, cfg).(type) {
	case nil:
		return nil
	case string:
		n, err := model.ParseInt(v)
		if err != nil {
			panic(fmt.Errorf("NumberFormatException: For input string: %q", v))
		}
		r = int(n)
	case int64:
		r = int(int32(v))
	case float64:
		r = int(javaDoubleToInt(v))
	default:
		panic(errJava)
	}
	return &r
}

// LookupBool is AttributeUtil.lookup for a Boolean key: nil when unset.
func (m *Message) LookupBool(deviceID int64, name string, server, cfg bool) *bool {
	var r bool
	switch v := m.lookup(deviceID, name, server, cfg).(type) {
	case nil:
		return nil
	case string:
		r = model.ParseBoolean(v)
	case bool:
		r = v
	default:
		panic(errJava) // ClassCastException
	}
	return &r
}

// LookupDouble is AttributeUtil.lookup for a Double key: nil when unset.
func (m *Message) LookupDouble(deviceID int64, name string, server, cfg bool) *float64 {
	var r float64
	switch v := m.lookup(deviceID, name, server, cfg).(type) {
	case nil:
		return nil
	case string:
		f, err := model.ParseDouble(v)
		if err != nil {
			panic(fmt.Errorf("NumberFormatException: For input string: %q", v))
		}
		r = f
	case int64:
		r = float64(v)
	case float64:
		r = v
	default:
		panic(errJava)
	}
	return &r
}

// javaDoubleToInt is Java's (int) cast of a double: NaN is 0, saturating at the int range.
func javaDoubleToInt(f float64) int32 {
	switch {
	case f != f:
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int32(f)
}

// UniqueID is BaseProtocolEncoder.getUniqueId: the device's unique id; an unknown device
// panics (Java's NullPointerException).
func (m *Message) UniqueID(deviceID int64) string {
	d, err := m.Device(deviceID)
	if err != nil {
		panic(err)
	}
	if d == nil {
		panic(errJava)
	}
	return d.UniqueID
}

// WriteMedia is writeMediaFile(uniqueId, buf, extension): the stored file's name, or ""
// (Java's null).
func (m *Message) WriteMedia(uniqueID, extension string, data []byte) string {
	return m.env.WriteMedia(uniqueID, extension, data)
}

// NewMediaBuffer is newMediaBuffer(): an empty buffer limited to media.bufferSize.
func (m *Message) NewMediaBuffer() *kit.Buf {
	size, err := m.Config().Int("media.bufferSize", 32*1024*1024)
	if err != nil {
		panic(err)
	}
	return kit.NewBufMax(size)
}

// Zone is the server's time zone, Java's ZoneId.systemDefault().
func (m *Message) Zone() *time.Location { return m.env.Zone() }

// DevicePassword is AttributeUtil.getDevicePassword: the device's devicePassword
// attribute, else <protocol>.devicePassword, else def.
func (m *Message) DevicePassword(deviceID int64, protocol, def string) string {
	switch v := m.lookup(deviceID, "devicePassword", false, false).(type) {
	case nil:
	case string:
		return v
	default:
		panic(errJava) // ClassCastException
	}
	if protocol != "" {
		if v, ok := m.Config().Lookup(protocol + ".devicePassword"); ok {
			return v
		}
	}
	return def
}
