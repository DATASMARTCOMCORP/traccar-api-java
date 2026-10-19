package attrs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Key is a ConfigKey that can be set per device (AttributeUtil.lookup): the device's
// attributes win, then its group's and up to two more ancestors', then the server's
// attributes when the key allows it, then the config file when the key allows it.
type Key struct {
	Name   string
	Server bool // KeyType.SERVER
	Config bool // KeyType.CONFIG
}

// ErrJava marks a failure where the Java code throws (a ClassCastException or
// NumberFormatException from a lookup, a geometry parse error...). The Java handler then
// logs it and moves on, so Go does the same with the handler that hit it.
var ErrJava = errors.New("java exception")

// Context is what processing (and reports) read about a device: the row, its group chain and the
// server row, read from the database when the position starts processing. Java reads the
// same from its cache, which the API keeps current; reading the database gives the same
// values without that cache, which lives in the process that serves the API.
type Context struct {
	St     *store.Store
	Cfg    *config.Config
	Device model.Device
	server *model.Server
	groups map[int64]*model.Group // read on demand; nil entries are missing groups
}

// New is the context of a device row.
func New(st *store.Store, cfg *config.Config, device model.Device) *Context {
	return &Context{St: st, Cfg: cfg, Device: device, groups: map[int64]*model.Group{}}
}

// Load reads the device row and returns its context.
func Load(ctx context.Context, st *store.Store, cfg *config.Config, deviceID int64) (*Context, error) {
	d, err := store.Get[model.Device](ctx, st, deviceID)
	if err != nil {
		return nil, err
	}
	return New(st, cfg, d), nil
}

func (dc *Context) Group(ctx context.Context, id int64) (*model.Group, error) {
	if g, ok := dc.groups[id]; ok {
		return g, nil
	}
	g, err := store.Get[model.Group](ctx, dc.St, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		dc.groups[id] = nil
		return nil, nil
	case err != nil:
		return nil, err
	}
	dc.groups[id] = &g
	return &g, nil
}

func (dc *Context) Server(ctx context.Context) (*model.Server, error) {
	if dc.server == nil {
		rows, err := store.All[model.Server](ctx, dc.St)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, errors.New("no server row")
		}
		dc.server = &rows[0]
	}
	return dc.server, nil
}

// maxGroupDepth is Storage.MAX_GROUP_DEPTH.
const maxGroupDepth = 3

// Raw is AttributeUtil.lookup before the type conversion: the first value found, or nil.
// Config values are strings.
func (dc *Context) Raw(ctx context.Context, k Key) (any, error) {
	if v, ok := dc.Device.Attributes.Get(k.Name); ok && v != nil {
		return v, nil
	}
	groupID := dc.Device.GroupID
	for depth := maxGroupDepth; groupID > 0 && depth > 0; depth-- {
		g, err := dc.Group(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if g == nil {
			break
		}
		if v, ok := g.Attributes.Get(k.Name); ok && v != nil {
			return v, nil
		}
		groupID = g.GroupID
	}
	if k.Server {
		srv, err := dc.Server(ctx)
		if err != nil {
			return nil, err
		}
		if v, ok := srv.Attributes.Get(k.Name); ok && v != nil {
			return v, nil
		}
	}
	if k.Config {
		if v, ok := dc.Cfg.Lookup(k.Name); ok {
			return v, nil
		}
	}
	return nil, nil
}

// String returns a String key's value. A non-string attribute makes Java's cast
// fail when the value is used.
func (dc *Context) String(ctx context.Context, k Key) (*string, error) {
	v, err := dc.Raw(ctx, k)
	if err != nil || v == nil {
		return nil, err
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: %w (not a String)", k.Name, ErrJava)
	}
	return &s, nil
}

// Bool returns a Boolean key's value: strings go through Boolean.parseBoolean,
// booleans are used as is, and anything else fails when Java unboxes it.
func (dc *Context) Bool(ctx context.Context, k Key) (*bool, error) {
	v, err := dc.Raw(ctx, k)
	if err != nil || v == nil {
		return nil, err
	}
	switch x := v.(type) {
	case string:
		b := model.ParseBoolean(x)
		return &b, nil
	case bool:
		return &x, nil
	}
	return nil, fmt.Errorf("%s: %w (not a Boolean)", k.Name, ErrJava)
}

// Long returns a Long (or, with bits 32, an Integer) key's value: strings go
// through Long.parseLong (Integer.parseInt), numbers through longValue (intValue).
func (dc *Context) Long(ctx context.Context, k Key, bits int) (*int64, error) {
	v, err := dc.Raw(ctx, k)
	if err != nil || v == nil {
		return nil, err
	}
	var n int64
	switch x := v.(type) {
	case string:
		if bits == 32 {
			i, perr := model.ParseInt(x)
			n, err = int64(i), perr
		} else {
			n, err = model.ParseLong(x)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w (%q is not a number)", k.Name, ErrJava, x)
		}
	case bool, []any, *model.Attributes:
		return nil, fmt.Errorf("%s: %w (not a Number)", k.Name, ErrJava)
	default:
		f, ok := NumberValue(x)
		if !ok {
			return nil, fmt.Errorf("%s: %w (not a Number)", k.Name, ErrJava)
		}
		n = JavaLongValue(x, f)
		if bits == 32 {
			n = int64(int32(n))
		}
	}
	return &n, nil
}

// Double returns a Double key's value: strings go through Double.parseDouble,
// numbers through doubleValue.
func (dc *Context) Double(ctx context.Context, k Key) (*float64, error) {
	v, err := dc.Raw(ctx, k)
	if err != nil || v == nil {
		return nil, err
	}
	if s, ok := v.(string); ok {
		f, perr := model.ParseDouble(s)
		if perr != nil {
			return nil, fmt.Errorf("%s: %w (%q is not a number)", k.Name, ErrJava, s)
		}
		return &f, nil
	}
	f, ok := NumberValue(v)
	if !ok {
		return nil, fmt.Errorf("%s: %w (not a Number)", k.Name, ErrJava)
	}
	return &f, nil
}

// NumberValue is Number.doubleValue for the number types attributes hold.
func NumberValue(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, true
	}
	return 0, false
}

// JavaLongValue is Number.longValue: integers as they are, doubles truncated toward zero
// and saturated (NaN is 0), as Java's (long) cast does.
func JavaLongValue(v any, f float64) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case *big.Int: // BigInteger.longValue: the low 64 bits, two's complement
		return int64(new(big.Int).And(x, new(big.Int).SetUint64(math.MaxUint64)).Uint64())
	}
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt64:
		return math.MaxInt64
	case f <= math.MinInt64:
		return math.MinInt64
	}
	return int64(f)
}

// LongOr is Long with the key's default.
func (dc *Context) LongOr(ctx context.Context, k Key, def int64) (int64, error) {
	v, err := dc.Long(ctx, k, 64)
	if err != nil || v == nil {
		return def, err
	}
	return *v, nil
}

// BoolOr is Bool with the key's default.
func (dc *Context) BoolOr(ctx context.Context, k Key, def bool) (bool, error) {
	v, err := dc.Bool(ctx, k)
	if err != nil || v == nil {
		return def, err
	}
	return *v, nil
}
