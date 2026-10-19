package reports

import (
	"context"
	"errors"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/attrs"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/motion"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Trips is TripsReportProvider.getObjects.
func (r *Reports) Trips(ctx context.Context, q Query) ([]model.TripReportItem, error) {
	out := []model.TripReportItem{}
	err := r.tripsAndStops(ctx, q, true, func(it any) { out = append(out, it.(model.TripReportItem)) })
	return out, err
}

// Stops is StopsReportProvider.getObjects.
func (r *Reports) Stops(ctx context.Context, q Query) ([]model.StopReportItem, error) {
	out := []model.StopReportItem{}
	err := r.tripsAndStops(ctx, q, false, func(it any) { out = append(out, it.(model.StopReportItem)) })
	return out, err
}

func (r *Reports) tripsAndStops(ctx context.Context, q Query, trips bool, add func(any)) error {
	if err := r.checkPeriodLimit(q); err != nil {
		return err
	}
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if err := r.detect(ctx, d, q.From, q.To, trips, add); err != nil {
			return err
		}
	}
	return nil
}

// motionEvent is a deviceMoving or deviceStopped event with the period's maximum speed.
type motionEvent struct {
	moving     bool
	positionID int64
	maxSpeed   float64
}

// detect is ReportUtils.detectTripsAndStops: from stored motion events for long periods
// (report.fastThreshold), else by replaying the motion logic over the positions.
func (r *Reports) detect(ctx context.Context, d model.Device, from, to model.Time, trips bool, add func(any)) error {
	dc := attrs.New(r.st, r.cfg, d)
	tc, err := motion.LoadTripsConfig(ctx, dc)
	if err != nil {
		return err
	}
	if int64(to.Time.Sub(from.Time).Seconds()) > r.fastThreshold {
		return r.fast(ctx, d, from, to, trips, tc, add)
	}

	var events []motionEvent
	positionMap := map[int64]*model.Position{}
	var start, last *model.Position
	maxSpeed := 0.0
	positions, err := r.positions(ctx, d.ID, from, to, 0)
	if err != nil {
		return err
	}
	if r.newTripLogic {
		cfg, err := motion.LoadConfig(ctx, dc)
		if err != nil {
			return err
		}
		var window []*model.Position
		var state motion.State
		for i := range positions {
			p := &positions[i]
			if last == nil {
				initial := p.Attributes.JavaBool(model.KeyMotion)
				if initial == trips {
					start, maxSpeed = p, p.Speed
				}
				state.Streak = initial
				state.SetEventPosition(p)
			}
			maxSpeed = max(maxSpeed, p.Speed)
			positionMap[p.ID] = p
			if err := motion.Update(&state, window, p, cfg); err != nil {
				return err
			}
			if len(state.Events) > 0 {
				for _, e := range state.Events {
					events = append(events, motionEvent{e.Type == model.EventDeviceMoving, e.PositionID, maxSpeed})
				}
				maxSpeed = 0
			}
			window = append(window, p)
			for len(window) > 1 && window[len(window)-1].FixTime.Time.UnixMilli()-window[1].FixTime.Time.UnixMilli() >= cfg.MinDuration {
				window = window[1:]
			}
			last = p
		}
	} else {
		var state motion.LegacyState
		for i := range positions {
			p := &positions[i]
			if last == nil {
				initial := p.Attributes.JavaBool(model.KeyMotion)
				if initial == trips {
					start, maxSpeed = p, p.Speed
				}
				state.Streak, state.State = initial, initial
			}
			maxSpeed = max(maxSpeed, p.Speed)
			positionMap[p.ID] = p
			if err := motion.UpdateLegacy(&state, last, p, p.Attributes.JavaBool(model.KeyMotion), tc); err != nil {
				return err
			}
			if e := state.Event; e != nil {
				events = append(events, motionEvent{e.Type == model.EventDeviceMoving, e.PositionID, maxSpeed})
				maxSpeed = 0
			}
			last = p
		}
	}

	for _, e := range events {
		if e.moving == trips {
			start = positionMap[e.positionID]
		} else if start != nil {
			if end := positionMap[e.positionID]; end != nil {
				if err := r.item(ctx, d, start, end, e.maxSpeed, tc.IgnoreOdometer, trips, add); err != nil {
					return err
				}
			}
			start = nil
		}
	}
	if start != nil {
		return r.item(ctx, d, start, last, maxSpeed, tc.IgnoreOdometer, trips, add)
	}
	return nil
}

// fast is ReportUtils.fastTripsAndStops: from the stored events, without speeds.
func (r *Reports) fast(ctx context.Context, d model.Device, from, to model.Time, trips bool, tc motion.TripsConfig, add func(any)) error {
	events, err := r.events(ctx, d.ID, from, to, model.EventDeviceMoving, model.EventDeviceStopped)
	if err != nil {
		return err
	}
	start, err := r.edgePosition(ctx, d.ID, from, to, false)
	if err != nil {
		return err
	}
	if start != nil && !start.Attributes.JavaBool(model.KeyMotion) {
		start = nil
	}
	for _, e := range events {
		moving := e.Type == model.EventDeviceMoving
		if moving == trips {
			if start, err = r.position(ctx, d.ID, e.PositionID); err != nil {
				return err
			}
		} else if start != nil {
			end, err := r.position(ctx, d.ID, e.PositionID)
			if err != nil {
				return err
			}
			if end != nil {
				if err := r.item(ctx, d, start, end, 0, tc.IgnoreOdometer, trips, add); err != nil {
					return err
				}
			}
			start = nil
		}
	}
	if start != nil {
		end, err := r.edgePosition(ctx, d.ID, from, to, true)
		if err != nil {
			return err
		}
		if end == nil {
			return errors.New("NullPointerException: no end position")
		}
		return r.item(ctx, d, start, end, 0, tc.IgnoreOdometer, trips, add)
	}
	return nil
}

func (r *Reports) item(ctx context.Context, d model.Device, start, end *model.Position, maxSpeed float64, ignoreOdometer, trips bool, add func(any)) error {
	if trips {
		t, err := r.trip(ctx, d, start, end, maxSpeed, ignoreOdometer)
		if err != nil {
			return err
		}
		add(t)
		return nil
	}
	s, err := r.stop(d, start, end, ignoreOdometer)
	if err != nil {
		return err
	}
	add(s)
	return nil
}

// trip is ReportUtils.calculateTrip.
func (r *Reports) trip(ctx context.Context, d model.Device, start, end *model.Position, maxSpeed float64, ignoreOdometer bool) (model.TripReportItem, error) {
	name := d.Name
	duration := end.FixTime.Time.UnixMilli() - start.FixTime.Time.UnixMilli()
	t := model.TripReportItem{
		ReportItem: model.ReportItem{DeviceID: start.DeviceID, DeviceName: &name, MaxSpeed: maxSpeed,
			StartTime: start.FixTime, EndTime: end.FixTime},
		StartPositionID: start.ID, StartLat: start.Latitude, StartLon: start.Longitude, StartAddress: r.address(start),
		EndPositionID: end.ID, EndLat: end.Latitude, EndLon: end.Longitude, EndAddress: r.address(end),
		Duration: duration,
	}
	var err error
	if t.Distance, err = distance(start, end, !ignoreOdometer); err != nil {
		return t, err
	}
	if duration > 0 {
		t.AverageSpeed = units.KnotsFromMps(t.Distance * 1000 / float64(duration))
	}
	if t.SpentFuel, err = fuel(start, end, &d); err != nil {
		return t, err
	}
	t.DriverUniqueID = driver(start, end)
	if t.DriverUniqueID != nil {
		drivers, err := store.List[model.Driver](ctx, r.st, store.ListOptions{
			Where: []store.Cond{store.Equals("uniqueId", *t.DriverUniqueID)}})
		if err != nil {
			return t, err
		}
		if len(drivers) > 0 {
			t.DriverName = &drivers[0].Name
		}
	}
	t.StartOdometer, t.EndOdometer, err = odometers(start, end, ignoreOdometer)
	return t, err
}

// driver is ReportUtils.findDriver.
func driver(first, last *model.Position) *string {
	if first.Attributes.Has(model.KeyDriverUniqueID) {
		return first.Attributes.JavaString(model.KeyDriverUniqueID)
	}
	if last.Attributes.Has(model.KeyDriverUniqueID) {
		return last.Attributes.JavaString(model.KeyDriverUniqueID)
	}
	return nil
}

// stop is ReportUtils.calculateStop.
func (r *Reports) stop(d model.Device, start, end *model.Position, ignoreOdometer bool) (model.StopReportItem, error) {
	name := d.Name
	s := model.StopReportItem{
		ReportItem: model.ReportItem{DeviceID: start.DeviceID, DeviceName: &name, StartTime: start.FixTime, EndTime: end.FixTime},
		PositionID: start.ID, Latitude: start.Latitude, Longitude: start.Longitude, Address: r.address(start),
		Duration: end.FixTime.Time.UnixMilli() - start.FixTime.Time.UnixMilli(),
	}
	var err error
	if s.SpentFuel, err = fuel(start, end, &d); err != nil {
		return s, err
	}
	if start.Attributes.Has(model.KeyHours) && end.Attributes.Has(model.KeyHours) {
		a, err := start.Attributes.JavaLong(model.KeyHours)
		if err != nil {
			return s, err
		}
		b, err := end.Attributes.JavaLong(model.KeyHours)
		if err != nil {
			return s, err
		}
		s.EngineHours = b - a
	}
	s.StartOdometer, s.EndOdometer, err = odometers(start, end, ignoreOdometer)
	return s, err
}
