package ingest

import (
	"context"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/motion"
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

// newMotion is handleNewLogic with NewMotionProcessor.updateState.
func (p *Processor) newMotion(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	cfg, err := motion.LoadConfig(ctx, dc)
	if err != nil {
		return nil, err
	}
	d := &dc.Device
	state := motion.State{Streak: d.MotionStreak}
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
		state.EventTime = model.NewTime(timeFromMillis(javaDoubleToLong(t.f)))
		if t.isLong {
			state.EventTime = model.NewTime(timeFromMillis(t.l))
		}
		state.EventLat, state.EventLon = lat.f, lon.f
		state.Changed = true
	case !d.MotionTime.IsZero():
		state.EventTime, state.EventLat, state.EventLon = d.MotionTime, d.MotionLatitude, d.MotionLongitude
	default:
		state.SetEventPosition(pos)
	}

	if err := motion.Update(&state, positions, pos, cfg); err != nil {
		return nil, err
	}

	if state.Changed {
		d.MotionStreak, d.MotionTime = state.Streak, state.EventTime
		d.MotionLatitude, d.MotionLongitude = state.EventLat, state.EventLon
		if err := p.st.UpdateDeviceMotion(ctx, d, migrated); err != nil {
			p.log.Warn("Update device motion error", "error", err)
		}
	}
	return state.Events, nil
}

// oldMotion is handleOldLogic with MotionProcessor.updateState and TripsConfig.
func (p *Processor) oldMotion(ctx context.Context, dc *deviceContext, pos *model.Position) ([]*model.Event, error) {
	cfg, err := motion.LoadTripsConfig(ctx, dc)
	if err != nil {
		return nil, err
	}
	d := &dc.Device
	before := motion.LegacyState{Streak: d.MotionStreak, State: d.MotionState, PositionID: d.MotionPositionID,
		Time: d.MotionTime, Distance: d.MotionDistance}
	s := before
	if err := motion.UpdateLegacy(&s, p.cache.last(d.ID), pos, pos.Attributes.JavaBool(model.KeyMotion), cfg); err != nil {
		return nil, err
	}
	event := s.Event
	s.Event = nil
	if s != before {
		d.MotionStreak, d.MotionState, d.MotionPositionID = s.Streak, s.State, s.PositionID
		d.MotionTime, d.MotionDistance = s.Time, s.Distance
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
