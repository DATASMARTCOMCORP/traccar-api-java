package ingest

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// process runs one position through the position handlers, the event handlers and
// post-processing (ProcessingHandler), then acknowledges it.
func (p *Processor) process(j job) {
	defer p.work.Done()
	ctx := p.ctx
	pos := j.pos
	dc, err := p.loadContext(ctx, pos.DeviceID)
	if err != nil {
		p.log.Warn("Position processing failed", "deviceId", pos.DeviceID, "error", err)
		j.done()
		p.cache.removeDevice(pos.DeviceID, pos)
		return
	}
	filtered := false
	for _, h := range p.handlers() {
		f, err := h(ctx, dc, pos)
		if err != nil {
			// BasePositionHandler.handlePosition: a failing handler is logged and skipped.
			p.log.Warn("Position handler failed", "deviceId", pos.DeviceID, "error", err)
			continue
		}
		if f {
			filtered = true
			break
		}
	}
	if !filtered {
		p.detectEvents(ctx, dc, pos)
		if err := p.postProcess(ctx, dc, pos); err != nil {
			p.log.Warn("Position handler failed", "deviceId", pos.DeviceID, "error", err)
		}
	}
	j.done()
	p.cache.removeDevice(pos.DeviceID, pos)
}

// positionHandler is a BasePositionHandler: it may change the position and may filter it
// out (true), which ends processing without storing it.
type positionHandler func(ctx context.Context, dc *deviceContext, pos *model.Position) (bool, error)

// handlers lists the enabled position handlers in ProcessingHandler's order. Computed
// attributes, geolocation, map matching, geocoding, speed limits and forwarding are not
// ported yet (warnUnported); production uses none of them on positions.
func (p *Processor) handlers() []positionHandler {
	hs := []positionHandler{p.outdated, p.timeHandler, p.hemisphereHandler, p.distanceHandler}
	if p.filterEnabled {
		hs = append(hs, p.filterHandler)
	}
	hs = append(hs, p.geofenceHandler, p.motionHandler, p.driverHandler)
	if p.copyAttributes {
		hs = append(hs, p.copyAttributesHandler)
	}
	return append(hs, p.engineHoursHandler, p.databaseHandler)
}

// gpsEpoch is OutdatedHandler.GPS_EPOCH.
var gpsEpoch = time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)

// outdated is OutdatedHandler: a position without a fix of its own takes the last one.
func (p *Processor) outdated(_ context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	if pos.Outdated {
		if last := p.cache.last(pos.DeviceID); last != nil {
			pos.FixTime, pos.Valid = last.FixTime, last.Valid
			pos.Latitude, pos.Longitude, pos.Altitude = last.Latitude, last.Longitude, last.Altitude
			pos.Speed, pos.Course, pos.Accuracy = last.Speed, last.Course, last.Accuracy
		} else {
			pos.FixTime = model.NewTime(gpsEpoch)
		}
		if pos.DeviceTime.IsZero() {
			pos.DeviceTime = pos.ServerTime
		}
	}
	return false, nil
}

var (
	rolloverCycle     = 1024 * 7 * 24 * time.Hour
	rolloverThreshold = rolloverCycle - 90*24*time.Hour
)

// timeHandler is TimeHandler: GPS week rollover correction, then time.override.
func (p *Processor) timeHandler(ctx context.Context, dc *deviceContext, pos *model.Position) (bool, error) {
	now := time.Now()
	if pos.DeviceTime.IsZero() {
		return false, errors.New("TimeHandler: deviceTime is null")
	}
	pos.DeviceTime = adjustRollover(now, pos.DeviceTime)
	if pos.FixTime.IsZero() {
		return false, errors.New("TimeHandler: fixTime is null")
	}
	pos.FixTime = adjustRollover(now, pos.FixTime)

	override, err := dc.lookupString(ctx, keyTimeOverride)
	if err != nil || override == nil {
		return false, err
	}
	if p.timeProtocols != nil && (pos.Protocol == nil || !p.timeProtocols.Contains(*pos.Protocol)) {
		return false, nil
	}
	if *override == "serverTime" {
		pos.DeviceTime, pos.FixTime = pos.ServerTime, pos.ServerTime
	} else {
		pos.FixTime = pos.DeviceTime
	}
	return false, nil
}

func adjustRollover(now time.Time, t model.Time) model.Time {
	at := t.Time
	for at.After(gpsEpoch) && now.Sub(at) > rolloverThreshold {
		at = at.Add(rolloverCycle)
	}
	return model.NewTime(at)
}

// hemisphereHandler is HemisphereHandler (location.latitudeHemisphere and
// location.longitudeHemisphere).
func (p *Processor) hemisphereHandler(_ context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	if p.latitudeFactor != 0 {
		pos.Latitude = math.Abs(pos.Latitude) * float64(p.latitudeFactor)
	}
	if p.longitudeFactor != 0 {
		pos.Longitude = math.Abs(pos.Longitude) * float64(p.longitudeFactor)
	}
	return false, nil
}

// distanceHandler is DistanceHandler: distance from the last position (unless the device
// reported one), the optional coordinates filter, and the running totalDistance.
func (p *Processor) distanceHandler(_ context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	distance := 0.0
	hasDistance := pos.Attributes.Has(model.KeyDistance)
	if hasDistance {
		d, err := pos.Attributes.JavaDouble(model.KeyDistance, 0)
		if err != nil {
			return false, err
		}
		distance = d
	}
	var totalDistance float64
	if last := p.cache.last(pos.DeviceID); last != nil {
		t, err := last.Attributes.JavaDouble(model.KeyTotalDistance, 0)
		if err != nil {
			return false, err
		}
		totalDistance = t
		if !hasDistance {
			distance = geo.Distance(pos.Latitude, pos.Longitude, last.Latitude, last.Longitude)
		}
		if p.coordinatesFilter && last.Latitude != 0 && last.Longitude != 0 {
			satisfiesMin := p.minError == 0 || distance > float64(p.minError)
			satisfiesMax := p.maxError == 0 || distance < float64(p.maxError)
			if !satisfiesMin || !satisfiesMax {
				pos.Valid, pos.Latitude, pos.Longitude = last.Valid, last.Latitude, last.Longitude
				distance = 0
			}
		}
	}
	pos.Attributes.Set(model.KeyDistance, distance)
	pos.Attributes.Set(model.KeyTotalDistance, totalDistance+distance)
	return false, nil
}

// geofenceHandler is GeofenceHandler: the ids of the device's geofences that contain the
// position. A geofence Java can't parse (or whose floor/ceiling isn't a number) makes
// the whole handler fail, so the position keeps no geofences.
func (p *Processor) geofenceHandler(ctx context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	geofences, err := p.st.DeviceGeofences(ctx, pos.DeviceID)
	if err != nil {
		return false, err
	}
	var ids []int64
	for i := range geofences {
		in, err := geo.GeofenceContains(&geofences[i], pos.Latitude, pos.Longitude, pos.Altitude)
		if err != nil {
			return false, err
		}
		if in {
			ids = append(ids, geofences[i].ID)
		}
	}
	if len(ids) > 0 {
		pos.GeofenceIDs = ids
	}
	return false, nil
}

// motionHandler is MotionHandler: motion is speed above event.motion.speedThreshold,
// unless the device reported it.
func (p *Processor) motionHandler(ctx context.Context, dc *deviceContext, pos *model.Position) (bool, error) {
	if pos.Attributes.Has(model.KeyMotion) {
		return false, nil
	}
	threshold, err := dc.lookupDouble(ctx, keyMotionThreshold)
	if err != nil {
		return false, err
	}
	t := 0.01
	if threshold != nil {
		t = *threshold
	}
	pos.Attributes.Set(model.KeyMotion, pos.Speed > t)
	return false, nil
}

// driverHandler is DriverHandler (processing.useLinkedDriver). Java takes any linked
// driver when there are several; Go takes the lowest id.
func (p *Processor) driverHandler(ctx context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	if !p.useLinkedDriver || pos.Attributes.Has(model.KeyDriverUniqueID) {
		return false, nil
	}
	drivers, err := p.st.DeviceDrivers(ctx, pos.DeviceID)
	if err != nil {
		return false, err
	}
	if len(drivers) > 0 {
		setString(&pos.Attributes, model.KeyDriverUniqueID, drivers[0].UniqueID)
	}
	return false, nil
}

// setString is ExtendedModel.set(String, String): null and empty values are not stored.
func setString(a *model.Attributes, key, value string) {
	if value != "" {
		a.Set(key, value)
	}
}

var spaceOrComma = regexp.MustCompile(`[ ,]`)

// copyAttributesHandler is CopyAttributesHandler: the listed attributes carry over from
// the last position when the new one lacks them.
func (p *Processor) copyAttributesHandler(ctx context.Context, dc *deviceContext, pos *model.Position) (bool, error) {
	list, err := dc.lookupString(ctx, keyCopyAttributes)
	if err != nil {
		return false, err
	}
	last := p.cache.last(pos.DeviceID)
	if last == nil || list == nil {
		return false, nil
	}
	for _, name := range javaSplitRegex(*list, spaceOrComma) {
		if v, ok := last.Attributes.Get(name); ok && !pos.Attributes.Has(name) {
			pos.Attributes.Set(name, v)
		}
	}
	return false, nil
}

// javaSplitRegex is String.split(regex): trailing empty strings are dropped, and an empty
// input gives one empty string.
func javaSplitRegex(s string, re *regexp.Regexp) []string {
	if s == "" {
		return []string{""}
	}
	parts := re.Split(s, -1)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// engineHoursHandler is EngineHoursHandler: engine hours accumulate while the ignition
// stays on, unless the device reports them.
func (p *Processor) engineHoursHandler(_ context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	if pos.Attributes.Has(model.KeyHours) {
		return false, nil
	}
	last := p.cache.last(pos.DeviceID)
	if last == nil {
		return false, nil
	}
	hours, err := last.Attributes.JavaLong(model.KeyHours)
	if err != nil {
		return false, err
	}
	if last.Attributes.JavaBool(model.KeyIgnition) && pos.Attributes.JavaBool(model.KeyIgnition) {
		hours += pos.DeviceTime.Time.UnixMilli() - last.DeviceTime.Time.UnixMilli()
	}
	if hours != 0 {
		pos.Attributes.Set(model.KeyHours, hours)
	}
	return false, nil
}

// databaseHandler is DatabaseHandler: the position is stored (a failure is logged and
// processing goes on with id 0).
func (p *Processor) databaseHandler(ctx context.Context, _ *deviceContext, pos *model.Position) (bool, error) {
	if err := store.Insert(ctx, p.st, pos); err != nil {
		p.log.Warn("Failed to store position", "deviceId", pos.DeviceID, "error", err)
		return false, nil
	}
	protocol := ""
	if pos.Protocol != nil {
		protocol = *pos.Protocol
	}
	p.stats.messageStored(ctx, pos.DeviceID, protocol)
	return false, nil
}

// isLatest is PositionUtil.isLatest.
func (p *Processor) isLatest(pos *model.Position) bool {
	last := p.cache.last(pos.DeviceID)
	return last == nil || !pos.FixTime.Time.Before(last.FixTime.Time)
}

// postProcess is PostProcessHandler: the latest position becomes the device's position.
func (p *Processor) postProcess(ctx context.Context, dc *deviceContext, pos *model.Position) error {
	if !p.isLatest(pos) {
		return nil
	}
	d := model.Device{ID: pos.DeviceID, PositionID: pos.ID}
	if err := p.st.UpdateDevicePosition(ctx, &d); err != nil {
		p.log.Warn("Failed to update device", "deviceId", pos.DeviceID, "error", err)
		return nil
	}
	return p.cache.update(ctx, dc, pos)
}
