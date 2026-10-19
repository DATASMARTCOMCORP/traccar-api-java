package ingest

import (
	"context"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/attrs"
)

// key and deviceContext are AttributeUtil's lookup (internal/attrs), shared with reports.
type (
	key           = attrs.Key
	deviceContext = attrs.Context
)

// errJava marks a failure where the Java code throws (see attrs.ErrJava).
var errJava = attrs.ErrJava

// Keys read through AttributeUtil.lookup during processing, with org.traccar.config.Keys'
// names and types. Defaults are applied by the callers, as Java's key defaults.
var (
	keyDecoderTimezone     = attrs.Key{Name: "decoder.timezone", Server: false, Config: true}
	keyTimeOverride        = attrs.Key{Name: "time.override", Server: false, Config: true}
	keyFilterInvalid       = attrs.Key{Name: "filter.invalid", Server: false, Config: true}
	keyFilterZero          = attrs.Key{Name: "filter.zero", Server: false, Config: true}
	keyFilterDuplicate     = attrs.Key{Name: "filter.duplicate", Server: false, Config: true}
	keyFilterOutdated      = attrs.Key{Name: "filter.outdated", Server: false, Config: true}
	keyFilterFuture        = attrs.Key{Name: "filter.future", Server: false, Config: true}
	keyFilterPast          = attrs.Key{Name: "filter.past", Server: false, Config: true}
	keyFilterAccuracy      = attrs.Key{Name: "filter.accuracy", Server: false, Config: true}
	keyFilterApproximate   = attrs.Key{Name: "filter.approximate", Server: false, Config: true}
	keyFilterStatic        = attrs.Key{Name: "filter.static", Server: false, Config: true}
	keyFilterDistance      = attrs.Key{Name: "filter.distance", Server: false, Config: true}
	keyFilterMaxSpeed      = attrs.Key{Name: "filter.maxSpeed", Server: false, Config: true}
	keyFilterMinPeriod     = attrs.Key{Name: "filter.minPeriod", Server: false, Config: true}
	keyFilterDailyLimit    = attrs.Key{Name: "filter.dailyLimit", Server: false, Config: true}
	keyFilterDailyInterval = attrs.Key{Name: "filter.dailyLimitInterval", Server: false, Config: true}
	keyFilterSkipLimit     = attrs.Key{Name: "filter.skipLimit", Server: false, Config: true}
	keyFilterSkipAttrs     = attrs.Key{Name: "filter.skipAttributes", Server: false, Config: true}
	keyMotionThreshold     = attrs.Key{Name: "event.motion.speedThreshold", Server: false, Config: true}
	keyCopyAttributes      = attrs.Key{Name: "processing.copyAttributes", Server: false, Config: true}
	keyTripMinDistance     = attrs.Key{Name: "report.trip.minDistance", Server: false, Config: true}
	keyTripMinDuration     = attrs.Key{Name: "report.trip.minDuration", Server: false, Config: true}
	keyTripStopGap         = attrs.Key{Name: "report.trip.stopGap", Server: false, Config: true}
	keyTripMinParking      = attrs.Key{Name: "report.trip.minimalParkingDuration", Server: false, Config: true}
	keyTripMinTripDuration = attrs.Key{Name: "report.trip.minimalTripDuration", Server: false, Config: true}
	keyTripMinTripDistance = attrs.Key{Name: "report.trip.minimalTripDistance", Server: false, Config: true}
	keyTripMinNoData       = attrs.Key{Name: "report.trip.minimalNoDataDuration", Server: false, Config: true}
	keyTripUseIgnition     = attrs.Key{Name: "report.trip.useIgnition", Server: false, Config: true}
	keySpeedLimit          = attrs.Key{Name: "speedLimit", Server: true, Config: false}
	keyFuelDropThreshold   = attrs.Key{Name: "fuelDropThreshold", Server: true, Config: false}
	keyFuelIncrease        = attrs.Key{Name: "fuelIncreaseThreshold", Server: true, Config: false}
	keyProximityEnter      = attrs.Key{Name: "proximityEnterDistance", Server: false, Config: false}
	keyProximityExit       = attrs.Key{Name: "proximityExitDistance", Server: false, Config: false}
	keyUnaccompanied       = attrs.Key{Name: "unaccompaniedDistance", Server: false, Config: false}
)

func (p *Processor) loadContext(ctx context.Context, deviceID int64) (*deviceContext, error) {
	return attrs.Load(ctx, p.st, p.cfg, deviceID)
}
