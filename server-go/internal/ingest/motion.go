package ingest

import (
	"context"
	"math"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// motionEvents is MotionEventHandler: deviceMoving and deviceStopped events, with the
// trip logic chosen by report.trip.newLogic.
func (p *Processor) motionEvents(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	if !p.isLatest(pos) {
		return nil, nil
	}
	if p.newTripLogic {
		return p.newMotion(ctx, dc, pos)
	}
	return p.oldMotion(ctx, dc, pos)
}

func (dc *deviceContext) longOr(ctx context.Context, k key, def int64) (int64, error) {
	v, err := dc.lookupLong(ctx, k, 64)
	if err != nil || v == nil {
		return def, err
	}
	return *v, nil
}

func (dc *deviceContext) boolOr(ctx context.Context, k key, def bool) (bool, error) {
	v, err := dc.lookupBool(ctx, k)
	if err != nil || v == nil {
		return def, err
	}
	return *v, nil
}

// motionState is NewMotionState.
type motionState struct {
	changed   bool
	streak    bool
	eventTime model.Time
	eventLat  float64
	eventLon  float64
	events    []*model.Event
}

func (s *motionState) setEventPosition(pos *model.Position) {
	s.eventTime, s.eventLat, s.eventLon = pos.FixTime, pos.Latitude, pos.Longitude
	s.changed = true
}

func (s *motionState) addEvent(eventType string, pos *model.Position) {
	e := &model.Event{Type: eventType, DeviceID: pos.DeviceID, PositionID: pos.ID, EventTime: pos.FixTime}
	s.events = append(s.events, e)
	s.setEventPosition(pos)
}

// newMotion is handleNewLogic with NewMotionProcessor.updateState.
func (p *Processor) newMotion(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	minDistanceL, err := dc.longOr(ctx, keyTripMinDistance, 200)
	if err != nil {
		return nil, err
	}
	minDurationS, err := dc.longOr(ctx, keyTripMinDuration, 180)
	if err != nil {
		return nil, err
	}
	stopGapS, err := dc.longOr(ctx, keyTripStopGap, 3600)
	if err != nil {
		return nil, err
	}
	minDistance := float64(minDistanceL)
	minDuration, stopGap := minDurationS*1000, stopGapS*1000

	d := &dc.device
	state := motionState{streak: d.MotionStreak}
	positions := p.cache.window(d.ID)
	migrated := false
	switch {
	case d.Attributes.Has("motionTime"):
		migrated = true
		// Java's "temporary migration path" from attributes to columns.
		t, err := removeNumber(&d.Attributes, "motionTime")
		if err != nil {
			return nil, err
		}
		lat, err := removeNumber(&d.Attributes, "motionLat")
		if err != nil {
			return nil, err
		}
		lon, err := removeNumber(&d.Attributes, "motionLon")
		if err != nil {
			return nil, err
		}
		state.eventTime = model.NewTime(timeFromMillis(javaDoubleToLong(t.f)))
		if t.isLong {
			state.eventTime = model.NewTime(timeFromMillis(t.l))
		}
		state.eventLat, state.eventLon = lat.f, lon.f
		state.changed = true
	case !d.MotionTime.IsZero():
		state.eventTime, state.eventLat, state.eventLon = d.MotionTime, d.MotionLatitude, d.MotionLongitude
	default:
		state.setEventPosition(pos)
	}

	if err := updateMotion(&state, positions, pos, minDistance, minDuration, stopGap); err != nil {
		return nil, err
	}

	if state.changed {
		d.MotionStreak, d.MotionTime = state.streak, state.eventTime
		d.MotionLatitude, d.MotionLongitude = state.eventLat, state.eventLon
		if err := p.st.UpdateDeviceMotion(ctx, d, migrated); err != nil {
			p.log.Warn("Update device motion error", "error", err)
		}
	}
	return state.events, nil
}

// updateMotion is NewMotionProcessor.updateState.
func updateMotion(state *motionState, positions []*model.Position, pos *model.Position, minDistance float64, minDuration, stopGap int64) error {
	if len(positions) == 0 {
		return nil
	}
	minAverageSpeed := minDistance / float64(minDuration)
	last := positions[len(positions)-1]
	gap := pos.FixTime.Time.UnixMilli() - last.FixTime.Time.UnixMilli()
	gapDistance, err := pos.Attributes.JavaDouble(model.KeyDistance, 0)
	if err != nil {
		return err
	}
	gapAverageSpeed := math.NaN()
	if gap > 0 {
		gapAverageSpeed = gapDistance / float64(gap)
	}
	if gap > stopGap && gapDistance >= minDistance && gapAverageSpeed > minAverageSpeed {
		if state.streak {
			state.addEvent(model.EventDeviceStopped, last)
		}
		state.addEvent(model.EventDeviceMoving, last)
		state.addEvent(model.EventDeviceStopped, pos)
		state.streak = false
		return nil
	}
	if state.streak {
		for i := len(positions) - 1; i >= 0; i-- {
			c := positions[i]
			if geo.Distance(c.Latitude, c.Longitude, pos.Latitude, pos.Longitude) >= minDistance {
				return nil
			}
		}
		oldest := positions[0]
		if pos.FixTime.Time.UnixMilli()-oldest.FixTime.Time.UnixMilli() >= minDuration {
			state.streak = false
			state.addEvent(model.EventDeviceStopped, pos)
		}
		return nil
	}
	if geo.Distance(state.eventLat, state.eventLon, pos.Latitude, pos.Longitude) >= minDistance {
		state.streak = true
		start, next := pos, pos
		for i := len(positions) - 1; i >= 0; i-- {
			c := positions[i]
			beforeStop := !c.FixTime.Time.After(state.eventTime.Time)
			legDistance := geo.Distance(c.Latitude, c.Longitude, next.Latitude, next.Longitude)
			legDuration := next.FixTime.Time.UnixMilli() - c.FixTime.Time.UnixMilli()
			legMoving := legDuration > 0 && legDistance/float64(legDuration) > minAverageSpeed
			if beforeStop || next != pos && !legMoving {
				break
			}
			start, next = c, c
		}
		state.addEvent(model.EventDeviceMoving, start)
	}
	return nil
}

// oldMotion is handleOldLogic with MotionProcessor.updateState and TripsConfig.
func (p *Processor) oldMotion(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	minTripDistance, err := dc.longOr(ctx, keyTripMinTripDistance, 500)
	if err != nil {
		return nil, err
	}
	minTripDuration, err := dc.longOr(ctx, keyTripMinTripDuration, 300)
	if err != nil {
		return nil, err
	}
	minParking, err := dc.longOr(ctx, keyTripMinParking, 300)
	if err != nil {
		return nil, err
	}
	minNoData, err := dc.longOr(ctx, keyTripMinNoData, 3600)
	if err != nil {
		return nil, err
	}
	useIgnition, err := dc.boolOr(ctx, keyTripUseIgnition, false)
	if err != nil {
		return nil, err
	}
	if _, err := dc.boolOr(ctx, key{"report.ignoreOdometer", false, true}, false); err != nil {
		return nil, err
	}
	minTripDuration, minParking, minNoData = minTripDuration*1000, minParking*1000, minNoData*1000

	d := &dc.device
	last := p.cache.last(d.ID)
	newState := pos.Attributes.JavaBool(model.KeyMotion)
	changed := false
	var event *model.Event
	totalDistance := func() (float64, error) { return pos.Attributes.JavaDouble(model.KeyTotalDistance, 0) }
	reset := func() {
		d.MotionPositionID, d.MotionTime, d.MotionDistance = 0, model.Time{}, 0
		changed = true
	}
	if last != nil && pos.FixTime.Time.UnixMilli()-last.FixTime.Time.UnixMilli() >= minNoData && d.MotionStreak {
		d.MotionStreak, d.MotionState = false, false
		reset()
		event = &model.Event{Type: model.EventDeviceStopped, DeviceID: last.DeviceID, PositionID: last.ID, EventTime: last.DeviceTime}
	} else if d.MotionState == newState {
		if !d.MotionTime.IsZero() {
			elapsed := pos.FixTime.Time.UnixMilli() - d.MotionTime.Time.UnixMilli()
			total, err := totalDistance()
			if err != nil {
				return nil, err
			}
			distance := total - d.MotionDistance
			var ignition *bool
			if useIgnition && pos.Attributes.Has(model.KeyIgnition) {
				v := pos.Attributes.JavaBool(model.KeyIgnition)
				ignition = &v
			}
			generate := false
			if newState {
				generate = elapsed >= minTripDuration || distance >= float64(minTripDistance)
			} else {
				generate = elapsed >= minParking || ignition != nil && !*ignition
			}
			if generate {
				eventType := model.EventDeviceStopped
				if newState {
					eventType = model.EventDeviceMoving
				}
				event = &model.Event{Type: eventType, DeviceID: pos.DeviceID, PositionID: d.MotionPositionID, EventTime: d.MotionTime}
				d.MotionStreak = newState
				reset()
			}
		}
	} else {
		d.MotionState = newState
		changed = true
		if d.MotionStreak == newState {
			reset()
		} else {
			total, err := totalDistance()
			if err != nil {
				return nil, err
			}
			d.MotionPositionID, d.MotionTime, d.MotionDistance = pos.ID, pos.FixTime, total
		}
	}
	if changed {
		if err := p.st.UpdateDeviceMotionLegacy(ctx, d); err != nil {
			p.log.Warn("Update device motion error", "error", err)
		}
	}
	if event != nil {
		return []*model.Event{event}, nil
	}
	return nil, nil
}

func timeFromMillis(ms int64) time.Time { return time.UnixMilli(ms) }

// number is an attribute value removed for the motion migration path.
type number struct {
	f      float64
	l      int64
	isLong bool
}

// removeNumber is ((Number) attributes.remove(key)): a missing or non-numeric value fails
// as Java's cast and unboxing do.
func removeNumber(a *model.Attributes, key string) (number, error) {
	v, ok := a.Get(key)
	a.Delete(key)
	if !ok || v == nil {
		return number{}, errJava
	}
	switch x := v.(type) {
	case int64:
		return number{f: float64(x), l: x, isLong: true}, nil
	case float64:
		return number{f: x}, nil
	}
	return number{}, errJava
}
