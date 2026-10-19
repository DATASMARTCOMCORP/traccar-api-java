// Package motion is Traccar's motion state machines, shared by event detection (deviceMoving
// and deviceStopped events) and the trips and stops reports: NewMotionProcessor
// (report.trip.newLogic) and the legacy MotionProcessor with TripsConfig.
package motion

import (
	"context"
	"math"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/attrs"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Config is the new logic's settings: report.trip.minDistance (meters), minDuration and
// stopGap (milliseconds).
type Config struct {
	MinDistance          float64
	MinDuration, StopGap int64
}

// LoadConfig looks the new logic's settings up for a device.
func LoadConfig(ctx context.Context, dc *attrs.Context) (Config, error) {
	minDistance, err := dc.LongOr(ctx, attrs.Key{Name: "report.trip.minDistance", Config: true}, 200)
	if err != nil {
		return Config{}, err
	}
	minDuration, err := dc.LongOr(ctx, attrs.Key{Name: "report.trip.minDuration", Config: true}, 180)
	if err != nil {
		return Config{}, err
	}
	stopGap, err := dc.LongOr(ctx, attrs.Key{Name: "report.trip.stopGap", Config: true}, 3600)
	if err != nil {
		return Config{}, err
	}
	return Config{MinDistance: float64(minDistance), MinDuration: minDuration * 1000, StopGap: stopGap * 1000}, nil
}

// State is NewMotionState.
type State struct {
	Changed   bool
	Streak    bool
	EventTime model.Time
	EventLat  float64
	EventLon  float64
	Events    []*model.Event // the events of the last Update
}

// SetEventPosition is setEventPosition.
func (s *State) SetEventPosition(pos *model.Position) {
	s.EventTime, s.EventLat, s.EventLon = pos.FixTime, pos.Latitude, pos.Longitude
	s.Changed = true
}

func (s *State) addEvent(eventType string, pos *model.Position) {
	e := &model.Event{Type: eventType, DeviceID: pos.DeviceID, PositionID: pos.ID, EventTime: pos.FixTime}
	s.Events = append(s.Events, e)
	s.SetEventPosition(pos)
}

// Update is NewMotionProcessor.updateState: positions is the recent window, oldest first.
func Update(state *State, positions []*model.Position, pos *model.Position, c Config) error {
	state.Events = nil
	if len(positions) == 0 {
		return nil
	}
	minAverageSpeed := c.MinDistance / float64(c.MinDuration)
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
	if gap > c.StopGap && gapDistance >= c.MinDistance && gapAverageSpeed > minAverageSpeed {
		if state.Streak {
			state.addEvent(model.EventDeviceStopped, last)
		}
		state.addEvent(model.EventDeviceMoving, last)
		state.addEvent(model.EventDeviceStopped, pos)
		state.Streak = false
		return nil
	}
	if state.Streak {
		for i := len(positions) - 1; i >= 0; i-- {
			c2 := positions[i]
			if geo.Distance(c2.Latitude, c2.Longitude, pos.Latitude, pos.Longitude) >= c.MinDistance {
				return nil
			}
		}
		oldest := positions[0]
		if pos.FixTime.Time.UnixMilli()-oldest.FixTime.Time.UnixMilli() >= c.MinDuration {
			state.Streak = false
			state.addEvent(model.EventDeviceStopped, pos)
		}
		return nil
	}
	if geo.Distance(state.EventLat, state.EventLon, pos.Latitude, pos.Longitude) >= c.MinDistance {
		state.Streak = true
		start, next := pos, pos
		for i := len(positions) - 1; i >= 0; i-- {
			c2 := positions[i]
			beforeStop := !c2.FixTime.Time.After(state.EventTime.Time)
			legDistance := geo.Distance(c2.Latitude, c2.Longitude, next.Latitude, next.Longitude)
			legDuration := next.FixTime.Time.UnixMilli() - c2.FixTime.Time.UnixMilli()
			legMoving := legDuration > 0 && legDistance/float64(legDuration) > minAverageSpeed
			if beforeStop || next != pos && !legMoving {
				break
			}
			start, next = c2, c2
		}
		state.addEvent(model.EventDeviceMoving, start)
	}
	return nil
}

// TripsConfig is the legacy logic's settings (TripsConfig): distances in meters, times in
// milliseconds.
type TripsConfig struct {
	MinTripDistance                        float64
	MinTripDuration, MinParking, MinNoData int64
	UseIgnition, IgnoreOdometer            bool
}

// LoadTripsConfig is new TripsConfig(provider) for a device: the first failing lookup
// fails, as Java's constructor throws.
func LoadTripsConfig(ctx context.Context, dc *attrs.Context) (TripsConfig, error) {
	var c TripsConfig
	longs := []struct {
		name string
		def  int64
		dst  *int64
	}{
		{"report.trip.minimalTripDistance", 500, nil},
		{"report.trip.minimalTripDuration", 300, &c.MinTripDuration},
		{"report.trip.minimalParkingDuration", 300, &c.MinParking},
		{"report.trip.minimalNoDataDuration", 3600, &c.MinNoData},
	}
	for _, l := range longs {
		v, err := dc.LongOr(ctx, attrs.Key{Name: l.name, Config: true}, l.def)
		if err != nil {
			return c, err
		}
		if l.dst == nil {
			c.MinTripDistance = float64(v)
		} else {
			*l.dst = v * 1000
		}
	}
	var err error
	if c.UseIgnition, err = dc.BoolOr(ctx, attrs.Key{Name: "report.trip.useIgnition", Config: true}, false); err != nil {
		return c, err
	}
	c.IgnoreOdometer, err = dc.BoolOr(ctx, attrs.Key{Name: "report.ignoreOdometer", Config: true}, false)
	return c, err
}

// LegacyState is MotionState.
type LegacyState struct {
	Streak     bool
	State      bool
	PositionID int64
	Time       model.Time
	Distance   float64
	Event      *model.Event // the event of the last Update
}

func (s *LegacyState) reset() {
	s.PositionID, s.Time, s.Distance = 0, model.Time{}, 0
}

// UpdateLegacy is MotionProcessor.updateState.
func UpdateLegacy(s *LegacyState, last, pos *model.Position, newState bool, c TripsConfig) error {
	s.Event = nil
	if last != nil && pos.FixTime.Time.UnixMilli()-last.FixTime.Time.UnixMilli() >= c.MinNoData && s.Streak {
		s.Streak, s.State = false, false
		s.reset()
		s.Event = &model.Event{Type: model.EventDeviceStopped, DeviceID: last.DeviceID, PositionID: last.ID, EventTime: last.DeviceTime}
		return nil
	}
	if s.State == newState {
		if s.Time.IsZero() {
			return nil
		}
		elapsed := pos.FixTime.Time.UnixMilli() - s.Time.Time.UnixMilli()
		total, err := pos.Attributes.JavaDouble(model.KeyTotalDistance, 0)
		if err != nil {
			return err
		}
		distance := total - s.Distance
		var ignition *bool
		if c.UseIgnition && pos.Attributes.Has(model.KeyIgnition) {
			v := pos.Attributes.JavaBool(model.KeyIgnition)
			ignition = &v
		}
		generate := false
		if newState {
			generate = elapsed >= c.MinTripDuration || distance >= c.MinTripDistance
		} else {
			generate = elapsed >= c.MinParking || ignition != nil && !*ignition
		}
		if generate {
			eventType := model.EventDeviceStopped
			if newState {
				eventType = model.EventDeviceMoving
			}
			s.Event = &model.Event{Type: eventType, DeviceID: pos.DeviceID, PositionID: s.PositionID, EventTime: s.Time}
			s.Streak = newState
			s.reset()
		}
		return nil
	}
	s.State = newState
	if s.Streak == newState {
		s.reset()
		return nil
	}
	total, err := pos.Attributes.JavaDouble(model.KeyTotalDistance, 0)
	if err != nil {
		return err
	}
	s.PositionID, s.Time, s.Distance = pos.ID, pos.FixTime, total
	return nil
}
