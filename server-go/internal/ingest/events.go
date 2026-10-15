package ingest

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// newEvent is new Event(type, position): the position's device, id and device time.
func newEvent(eventType string, pos *model.Position) *model.Event {
	return &model.Event{Type: eventType, DeviceID: pos.DeviceID, PositionID: pos.ID, EventTime: pos.DeviceTime}
}

// newDeviceEvent is new Event(type, deviceId): stamped now.
func newDeviceEvent(eventType string, deviceID int64) *model.Event {
	return &model.Event{Type: eventType, DeviceID: deviceID, EventTime: model.NewTime(time.Now())}
}

// storeEvents is NotificationManager.updateEvents: each event is stored, in order, with
// the device cached meanwhile. Forwarding and notifications are Step 9.
func (p *Processor) storeEvents(ctx context.Context, events []*model.Event, pos *model.Position) {
	for _, e := range events {
		ref := new(byte)
		if err := p.cache.addDevice(ctx, e.DeviceID, ref); err != nil {
			p.log.Warn("Event save error", "error", err)
			continue
		}
		if err := store.Insert(ctx, p.st, e); err != nil {
			p.log.Warn("Event save error", "error", err)
		}
		p.log.Info("Event", "deviceId", e.DeviceID, "time", e.EventTime.Time, "type", e.Type)
		p.cache.removeDevice(e.DeviceID, ref)
	}
}

// eventHandler is a BaseEventHandler; it returns the events it detects, in order.
type eventHandler func(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error)

// detectEvents runs the event handlers in ProcessingHandler's order. Each event is stored
// as soon as its handler reports it, so ids follow detection order; a failing handler is
// logged and the others still run.
func (p *Processor) detectEvents(ctx context.Context, dc *deviceContext, pos *model.Position) {
	handlers := []eventHandler{
		p.mediaEvents, p.commandResultEvents, p.overspeedEvents, p.behaviorEvents, p.fuelEvents,
		p.motionEvents, p.geofenceEvents, p.proximityEvents, p.alarmEvents, p.ignitionEvents,
		p.maintenanceEvents, p.driverEvents,
	}
	for _, h := range handlers {
		events, err := h(ctx, dc, pos)
		for _, e := range events {
			p.storeEvents(ctx, []*model.Event{e}, pos)
		}
		if err != nil {
			p.log.Warn("Event handler failed", "deviceId", pos.DeviceID, "error", err)
		}
	}
}

// mediaEvents is MediaEventHandler: one event per image, video or audio attribute.
func (p *Processor) mediaEvents(_ context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	var events []*model.Event
	for _, kind := range []string{model.KeyImage, model.KeyVideo, model.KeyAudio} {
		if !pos.Attributes.Has(kind) {
			continue
		}
		e := newEvent(model.EventMedia, pos)
		setString(&e.Attributes, "media", kind)
		if file := pos.Attributes.JavaString(kind); file != nil {
			setString(&e.Attributes, "file", *file)
		}
		events = append(events, e)
	}
	return events, nil
}

// commandResultEvents is CommandResultEventHandler. Java casts the result to String, so
// any other type fails the handler.
func (p *Processor) commandResultEvents(_ context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	v, ok := pos.Attributes.Get(model.KeyResult)
	if !ok || v == nil {
		return nil, nil
	}
	result, ok := v.(string)
	if !ok {
		return nil, errJava
	}
	e := newEvent(model.EventCommandResult, pos)
	setString(&e.Attributes, model.KeyResult, result)
	return []*model.Event{e}, nil
}

// overspeedEvents is OverspeedEventHandler with OverspeedProcessor.
func (p *Processor) overspeedEvents(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) {
		return nil, nil
	}
	limit, err := dc.lookupDouble(ctx, keySpeedLimit)
	if err != nil {
		return nil, err
	}
	speedLimit := 0.0
	if limit != nil {
		speedLimit = *limit
	}
	positionLimit, err := pos.Attributes.JavaDouble(model.KeySpeedLimit, 0)
	if err != nil {
		return nil, err
	}
	if positionLimit > 0 {
		speedLimit = positionLimit
	}
	geofenceLimit := 0.0
	var overspeedGeofence int64
	if pos.GeofenceIDs != nil {
		for _, id := range pos.GeofenceIDs {
			g, err := store.Get[model.Geofence](ctx, p.st, id)
			if err != nil {
				continue
			}
			current, err := g.Attributes.JavaDouble(keySpeedLimit.name, 0)
			if err != nil {
				return nil, err
			}
			if current > 0 && geofenceLimit == 0 ||
				p.overspeedPreferLowest && current < geofenceLimit ||
				!p.overspeedPreferLowest && current > geofenceLimit {
				geofenceLimit, overspeedGeofence = current, id
			}
		}
	}
	if geofenceLimit > 0 {
		speedLimit = geofenceLimit
	}
	if speedLimit == 0 {
		return nil, nil
	}

	d := dc.device
	changed := false
	var event *model.Event
	check := func() {
		if d.OverspeedTime.IsZero() {
			return
		}
		if pos.FixTime.Time.UnixMilli()-d.OverspeedTime.Time.UnixMilli() >= p.overspeedMinDuration {
			event = newEvent(model.EventDeviceOverspeed, pos)
			event.Attributes.Set("speed", pos.Speed)
			event.Attributes.Set(model.KeySpeedLimit, speedLimit)
			event.GeofenceID = d.OverspeedGeofenceID
			d.OverspeedTime, d.OverspeedGeofenceID = model.Time{}, 0
			changed = true
		}
	}
	over := pos.Speed > float64(speedLimit*p.overspeedMultiplier)
	if d.OverspeedState {
		if over {
			check()
		} else {
			d.OverspeedState, d.OverspeedTime, d.OverspeedGeofenceID = false, model.Time{}, 0
			changed = true
		}
	} else if over {
		d.OverspeedState, d.OverspeedTime, d.OverspeedGeofenceID = true, pos.FixTime, overspeedGeofence
		changed = true
		check()
	}
	if changed {
		dc.device = d
		if err := p.st.UpdateDeviceOverspeed(ctx, &d); err != nil {
			p.log.Warn("Update device overspeed error", "error", err)
		}
	}
	if event != nil {
		return []*model.Event{event}, nil
	}
	return nil, nil
}

// behaviorEvents is BehaviorEventHandler: hard acceleration and braking alarms.
func (p *Processor) behaviorEvents(_ context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	last := p.cache.last(pos.DeviceID)
	if last == nil || pos.FixTime.Time.Equal(last.FixTime.Time) {
		return nil, nil
	}
	acceleration := float64(units.MpsFromKnots(pos.Speed-last.Speed)*1000) /
		float64(pos.FixTime.Time.UnixMilli()-last.FixTime.Time.UnixMilli())
	var alarm string
	switch {
	case p.accelerationThreshold != 0 && acceleration >= p.accelerationThreshold:
		alarm = model.AlarmAcceleration
	case p.brakingThreshold != 0 && acceleration <= -p.brakingThreshold:
		alarm = model.AlarmBraking
	default:
		return nil, nil
	}
	e := newEvent(model.EventAlarm, pos)
	setString(&e.Attributes, model.KeyAlarm, alarm)
	return []*model.Event{e}, nil
}

// fuelEvents is FuelEventHandler: fuel drops and increases beyond the thresholds.
func (p *Processor) fuelEvents(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) || !pos.Attributes.Has(model.KeyFuel) {
		return nil, nil
	}
	last := p.cache.last(pos.DeviceID)
	if last == nil || !last.Attributes.Has(model.KeyFuel) {
		return nil, nil
	}
	before, err := last.Attributes.JavaDouble(model.KeyFuel, 0)
	if err != nil {
		return nil, err
	}
	after, err := pos.Attributes.JavaDouble(model.KeyFuel, 0)
	if err != nil {
		return nil, err
	}
	change := after - before
	var k key
	var eventType string
	switch {
	case change > 0:
		k, eventType = keyFuelIncrease, model.EventDeviceFuelIncrease
	case change < 0:
		k, eventType = keyFuelDropThreshold, model.EventDeviceFuelDrop
	default:
		return nil, nil
	}
	threshold, err := dc.lookupDouble(ctx, k)
	if err != nil {
		return nil, err
	}
	t := 0.0
	if threshold != nil {
		t = *threshold
	}
	if t > 0 && math.Abs(change) >= t {
		e := newEvent(eventType, pos)
		e.Attributes.Set("before", before)
		e.Attributes.Set("after", after)
		return []*model.Event{e}, nil
	}
	return nil, nil
}

// geofenceEvents is GeofenceEventHandler: exits for geofences the last position was in
// and this one isn't, then enters, each in Java's HashSet<Long> order. Geofences outside
// their calendar's schedule don't count.
func (p *Processor) geofenceEvents(ctx context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) {
		return nil, nil
	}
	oldGeofences := model.NewLongSet()
	if last := p.cache.last(pos.DeviceID); last != nil && last.GeofenceIDs != nil {
		for _, id := range last.GeofenceIDs {
			oldGeofences.Add(id)
		}
	}
	newGeofences := model.NewLongSet()
	if pos.GeofenceIDs != nil {
		for _, id := range pos.GeofenceIDs {
			newGeofences.Add(id)
		}
		for _, id := range oldGeofences.Items() {
			newGeofences.Remove(id)
		}
		for _, id := range pos.GeofenceIDs {
			oldGeofences.Remove(id)
		}
	}
	var events []*model.Event
	inSchedule := func(g model.Geofence) (bool, error) {
		if g.CalendarID == 0 {
			return true, nil
		}
		return p.checkCalendar(ctx, g.CalendarID, pos.FixTime.Time)
	}
	for _, id := range oldGeofences.Items() {
		g, err := store.Get[model.Geofence](ctx, p.st, id)
		if err != nil {
			continue // Java skips geofences it doesn't have
		}
		ok, err := inSchedule(g)
		if err != nil {
			return events, err
		}
		if ok {
			e := newEvent(model.EventGeofenceExit, pos)
			e.GeofenceID = id
			events = append(events, e)
		}
	}
	for _, id := range newGeofences.Items() {
		g, err := store.Get[model.Geofence](ctx, p.st, id)
		if err != nil {
			return events, err // Java dereferences the geofence without a check
		}
		ok, err := inSchedule(g)
		if err != nil {
			return events, err
		}
		if ok {
			e := newEvent(model.EventGeofenceEnter, pos)
			e.GeofenceID = id
			events = append(events, e)
		}
	}
	return events, nil
}

// proximityEvents is ProximityEventHandler: distance to linked devices that are cached
// (Java only knows the positions of devices it holds in its cache).
func (p *Processor) proximityEvents(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) {
		return nil, nil
	}
	linked, err := p.st.LinkedDevices(ctx, pos.DeviceID)
	if err != nil || len(linked) == 0 {
		return nil, err
	}
	distance := func(k key) (float64, error) {
		v, err := dc.lookupDouble(ctx, k)
		if err != nil || v == nil {
			return 0, err
		}
		return *v, nil
	}
	enter, err := distance(keyProximityEnter)
	if err != nil {
		return nil, err
	}
	exit, err := distance(keyProximityExit)
	if err != nil {
		return nil, err
	}
	unaccompanied, err := distance(keyUnaccompanied)
	if err != nil {
		return nil, err
	}
	last := p.cache.last(pos.DeviceID)
	if last == nil {
		return nil, nil
	}
	checkUnaccompanied := unaccompanied > 0 && !last.Attributes.JavaBool(model.KeyMotion) && pos.Attributes.JavaBool(model.KeyMotion)
	if enter <= 0 && exit <= 0 && !checkUnaccompanied {
		return nil, nil
	}
	maxDistance := math.Max(math.Max(enter, exit), unaccompanied)
	latDelta := geo.LatitudeDelta(maxDistance)
	lonDelta := geo.LongitudeDelta(maxDistance, pos.Latitude)
	bounded := func(from, to *model.Position) float64 {
		if math.Abs(from.Latitude-to.Latitude) > latDelta || math.Abs(from.Longitude-to.Longitude) > lonDelta {
			return math.Inf(1)
		}
		return geo.Distance(from.Latitude, from.Longitude, to.Latitude, to.Longitude)
	}
	var events []*model.Event
	anyAccompanied := false
	for _, id := range linked {
		other := p.cache.last(id)
		if other == nil {
			continue
		}
		dNew, dOld := bounded(pos, other), bounded(last, other)
		switch {
		case enter > 0 && dOld > enter && dNew <= enter:
			e := newEvent(model.EventProximityEnter, pos)
			e.Attributes.Set("linkedDeviceId", id)
			events = append(events, e)
		case exit > 0 && dOld <= exit && dNew > exit:
			e := newEvent(model.EventProximityExit, pos)
			e.Attributes.Set("linkedDeviceId", id)
			events = append(events, e)
		}
		if dNew <= unaccompanied {
			anyAccompanied = true
		}
	}
	if checkUnaccompanied && !anyAccompanied {
		events = append(events, newEvent(model.EventUnaccompanied, pos))
	}
	return events, nil
}

// alarmEvents is AlarmEventHandler: one event per alarm in the comma-separated list, in
// HashSet<String> order, minus the last position's alarms (event.ignoreDuplicateAlerts).
func (p *Processor) alarmEvents(_ context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	text := pos.Attributes.JavaString(model.KeyAlarm)
	if text == nil {
		return nil, nil
	}
	alarms := model.NewHashSetOf(javaSplitString(*text, ","))
	if p.ignoreDuplicateAlerts {
		if last := p.cache.last(pos.DeviceID); last != nil {
			if lastText := last.Attributes.JavaString(model.KeyAlarm); lastText != nil {
				for _, a := range javaSplitString(*lastText, ",") {
					alarms.Remove(a)
				}
			}
		}
	}
	var events []*model.Event
	for _, a := range alarms.Items() {
		e := newEvent(model.EventAlarm, pos)
		setString(&e.Attributes, model.KeyAlarm, a)
		events = append(events, e)
	}
	return events, nil
}

// javaSplitString is String.split for a literal one-character separator.
func javaSplitString(s, sep string) []string {
	if s == "" {
		return []string{""}
	}
	parts := strings.Split(s, sep)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// ignitionEvents is IgnitionEventHandler.
func (p *Processor) ignitionEvents(_ context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) || !pos.Attributes.Has(model.KeyIgnition) {
		return nil, nil
	}
	ignition := pos.Attributes.JavaBool(model.KeyIgnition)
	last := p.cache.last(pos.DeviceID)
	if last == nil || !last.Attributes.Has(model.KeyIgnition) {
		return nil, nil
	}
	old := last.Attributes.JavaBool(model.KeyIgnition)
	switch {
	case ignition && !old:
		return []*model.Event{newEvent(model.EventIgnitionOn, pos)}, nil
	case !ignition && old:
		return []*model.Event{newEvent(model.EventIgnitionOff, pos)}, nil
	}
	return nil, nil
}

// maintenanceEvents is MaintenanceEventHandler: an event each time a maintenance value
// crosses its next period boundary. Java goes through the maintenances in a set order
// that changes between runs; Go uses id order.
func (p *Processor) maintenanceEvents(ctx context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	last := p.cache.last(pos.DeviceID)
	if last == nil || pos.FixTime.Time.Before(last.FixTime.Time) {
		return nil, nil
	}
	maintenances, err := p.st.DeviceMaintenances(ctx, pos.DeviceID)
	if err != nil {
		return nil, err
	}
	var events []*model.Event
	for _, m := range maintenances {
		if m.Period == 0 {
			continue
		}
		oldValue, err := maintenanceValue(last, m.Type)
		if err != nil {
			return events, err
		}
		newValue, err := maintenanceValue(pos, m.Type)
		if err != nil {
			return events, err
		}
		if oldValue != 0 && newValue != 0 && newValue >= m.Start {
			if oldValue < m.Start ||
				javaDoubleToLong(float64(oldValue-m.Start)/m.Period) < javaDoubleToLong(float64(newValue-m.Start)/m.Period) {
				e := newEvent(model.EventMaintenance, pos)
				e.MaintenanceID = m.ID
				e.Attributes.Set(m.Type, newValue)
				events = append(events, e)
			}
		}
	}
	return events, nil
}

func maintenanceValue(pos *model.Position, kind string) (float64, error) {
	switch kind {
	case "serverTime":
		return float64(pos.ServerTime.Time.UnixMilli()), nil
	case "deviceTime":
		return float64(pos.DeviceTime.Time.UnixMilli()), nil
	case "fixTime":
		return float64(pos.FixTime.Time.UnixMilli()), nil
	}
	return pos.Attributes.JavaDouble(kind, 0)
}

// javaDoubleToLong is Java's (long) cast of a double.
func javaDoubleToLong(f float64) int64 {
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

// driverEvents is DriverEventHandler: an event when the driver id changes.
func (p *Processor) driverEvents(_ context.Context, _ *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) {
		return nil, nil
	}
	driver := pos.Attributes.JavaString(model.KeyDriverUniqueID)
	if driver == nil {
		return nil, nil
	}
	if last := p.cache.last(pos.DeviceID); last != nil {
		if old := last.Attributes.JavaString(model.KeyDriverUniqueID); old != nil && *old == *driver {
			return nil, nil
		}
	}
	e := newEvent(model.EventDriverChanged, pos)
	setString(&e.Attributes, model.KeyDriverUniqueID, *driver)
	return []*model.Event{e}, nil
}
