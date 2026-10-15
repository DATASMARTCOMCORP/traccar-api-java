package ingest

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

// A key is a ConfigKey that can be set per device (AttributeUtil.lookup): the device's
// attributes win, then its group's and up to two more ancestors', then the server's
// attributes when the key allows it, then the config file when the key allows it.
type key struct {
	name   string
	server bool // KeyType.SERVER
	config bool // KeyType.CONFIG
}

// Keys read through AttributeUtil.lookup during processing, with org.traccar.config.Keys'
// names and types. Defaults are applied by the callers, as Java's key defaults.
var (
	keyDecoderTimezone     = key{"decoder.timezone", false, true}
	keyTimeOverride        = key{"time.override", false, true}
	keyFilterInvalid       = key{"filter.invalid", false, true}
	keyFilterZero          = key{"filter.zero", false, true}
	keyFilterDuplicate     = key{"filter.duplicate", false, true}
	keyFilterOutdated      = key{"filter.outdated", false, true}
	keyFilterFuture        = key{"filter.future", false, true}
	keyFilterPast          = key{"filter.past", false, true}
	keyFilterAccuracy      = key{"filter.accuracy", false, true}
	keyFilterApproximate   = key{"filter.approximate", false, true}
	keyFilterStatic        = key{"filter.static", false, true}
	keyFilterDistance      = key{"filter.distance", false, true}
	keyFilterMaxSpeed      = key{"filter.maxSpeed", false, true}
	keyFilterMinPeriod     = key{"filter.minPeriod", false, true}
	keyFilterDailyLimit    = key{"filter.dailyLimit", false, true}
	keyFilterDailyInterval = key{"filter.dailyLimitInterval", false, true}
	keyFilterSkipLimit     = key{"filter.skipLimit", false, true}
	keyFilterSkipAttrs     = key{"filter.skipAttributes", false, true}
	keyMotionThreshold     = key{"event.motion.speedThreshold", false, true}
	keyCopyAttributes      = key{"processing.copyAttributes", false, true}
	keyTripMinDistance     = key{"report.trip.minDistance", false, true}
	keyTripMinDuration     = key{"report.trip.minDuration", false, true}
	keyTripStopGap         = key{"report.trip.stopGap", false, true}
	keyTripMinParking      = key{"report.trip.minimalParkingDuration", false, true}
	keyTripMinTripDuration = key{"report.trip.minimalTripDuration", false, true}
	keyTripMinTripDistance = key{"report.trip.minimalTripDistance", false, true}
	keyTripMinNoData       = key{"report.trip.minimalNoDataDuration", false, true}
	keyTripUseIgnition     = key{"report.trip.useIgnition", false, true}
	keySpeedLimit          = key{"speedLimit", true, false}
	keyFuelDropThreshold   = key{"fuelDropThreshold", true, false}
	keyFuelIncrease        = key{"fuelIncreaseThreshold", true, false}
	keyProximityEnter      = key{"proximityEnterDistance", false, false}
	keyProximityExit       = key{"proximityExitDistance", false, false}
	keyUnaccompanied       = key{"unaccompaniedDistance", false, false}
)

// errJava marks a failure where the Java code throws (a ClassCastException or
// NumberFormatException from a lookup, a geometry parse error...). The Java handler then
// logs it and moves on, so Go does the same with the handler that hit it.
var errJava = errors.New("java exception")

// deviceContext is what processing reads about a device: the row, its group chain and the
// server row, read from the database when the position starts processing. Java reads the
// same from its cache, which the API keeps current; reading the database gives the same
// values without that cache, which lives in the process that serves the API.
type deviceContext struct {
	st     *store.Store
	cfg    *config.Config
	device model.Device
	server *model.Server
	groups map[int64]*model.Group // read on demand; nil entries are missing groups
}

func (p *Processor) loadContext(ctx context.Context, deviceID int64) (*deviceContext, error) {
	d, err := store.Get[model.Device](ctx, p.st, deviceID)
	if err != nil {
		return nil, err
	}
	return &deviceContext{st: p.st, cfg: p.cfg, device: d, groups: map[int64]*model.Group{}}, nil
}

func (dc *deviceContext) group(ctx context.Context, id int64) (*model.Group, error) {
	if g, ok := dc.groups[id]; ok {
		return g, nil
	}
	g, err := store.Get[model.Group](ctx, dc.st, id)
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

func (dc *deviceContext) serverRow(ctx context.Context) (*model.Server, error) {
	if dc.server == nil {
		rows, err := store.All[model.Server](ctx, dc.st)
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

// raw is AttributeUtil.lookup before the type conversion: the first value found, or nil.
// Config values are strings.
func (dc *deviceContext) raw(ctx context.Context, k key) (any, error) {
	if v, ok := dc.device.Attributes.Get(k.name); ok && v != nil {
		return v, nil
	}
	groupID := dc.device.GroupID
	for depth := maxGroupDepth; groupID > 0 && depth > 0; depth-- {
		g, err := dc.group(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if g == nil {
			break
		}
		if v, ok := g.Attributes.Get(k.name); ok && v != nil {
			return v, nil
		}
		groupID = g.GroupID
	}
	if k.server {
		srv, err := dc.serverRow(ctx)
		if err != nil {
			return nil, err
		}
		if v, ok := srv.Attributes.Get(k.name); ok && v != nil {
			return v, nil
		}
	}
	if k.config {
		if v, ok := dc.cfg.Lookup(k.name); ok {
			return v, nil
		}
	}
	return nil, nil
}

// lookupString returns a String key's value. A non-string attribute makes Java's cast
// fail when the value is used.
func (dc *deviceContext) lookupString(ctx context.Context, k key) (*string, error) {
	v, err := dc.raw(ctx, k)
	if err != nil || v == nil {
		return nil, err
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: %w (not a String)", k.name, errJava)
	}
	return &s, nil
}

// lookupBool returns a Boolean key's value: strings go through Boolean.parseBoolean,
// booleans are used as is, and anything else fails when Java unboxes it.
func (dc *deviceContext) lookupBool(ctx context.Context, k key) (*bool, error) {
	v, err := dc.raw(ctx, k)
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
	return nil, fmt.Errorf("%s: %w (not a Boolean)", k.name, errJava)
}

// lookupLong returns a Long (or, with bits 32, an Integer) key's value: strings go
// through Long.parseLong (Integer.parseInt), numbers through longValue (intValue).
func (dc *deviceContext) lookupLong(ctx context.Context, k key, bits int) (*int64, error) {
	v, err := dc.raw(ctx, k)
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
			return nil, fmt.Errorf("%s: %w (%q is not a number)", k.name, errJava, x)
		}
	case bool, []any, *model.Attributes:
		return nil, fmt.Errorf("%s: %w (not a Number)", k.name, errJava)
	default:
		f, ok := numberValue(x)
		if !ok {
			return nil, fmt.Errorf("%s: %w (not a Number)", k.name, errJava)
		}
		n = javaLongValue(x, f)
		if bits == 32 {
			n = int64(int32(n))
		}
	}
	return &n, nil
}

// lookupDouble returns a Double key's value: strings go through Double.parseDouble,
// numbers through doubleValue.
func (dc *deviceContext) lookupDouble(ctx context.Context, k key) (*float64, error) {
	v, err := dc.raw(ctx, k)
	if err != nil || v == nil {
		return nil, err
	}
	if s, ok := v.(string); ok {
		f, perr := model.ParseDouble(s)
		if perr != nil {
			return nil, fmt.Errorf("%s: %w (%q is not a number)", k.name, errJava, s)
		}
		return &f, nil
	}
	f, ok := numberValue(v)
	if !ok {
		return nil, fmt.Errorf("%s: %w (not a Number)", k.name, errJava)
	}
	return &f, nil
}

// numberValue is Number.doubleValue for the number types attributes hold.
func numberValue(v any) (float64, bool) {
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

// javaLongValue is Number.longValue: integers as they are, doubles truncated toward zero
// and saturated (NaN is 0), as Java's (long) cast does.
func javaLongValue(v any, f float64) int64 {
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
