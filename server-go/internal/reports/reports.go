// Package reports computes the API's reports (org.traccar.reports): route, events,
// geofence stays, summary, trips, stops, combined and devices.
package reports

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/attrs"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/motion"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Reports reads report data. Its settings are read once, as Java's providers do.
type Reports struct {
	st  *store.Store
	cfg *config.Config

	periodLimit      time.Duration // report.periodLimit; 0: none
	fastThreshold    int64         // report.fastThreshold, seconds
	newTripLogic     bool          // report.trip.newLogic
	maxPositions     int           // report.maxPositions
	geocodeOnRequest bool          // geocoder.enable with pluscodes, and geocoder.onRequest
}

// New reads the report settings.
func New(cfg *config.Config, st *store.Store, maxPositions int) (*Reports, error) {
	limit, err := cfg.Int64("report.periodLimit", 0)
	if err != nil {
		return nil, err
	}
	fast, err := cfg.Int64("report.fastThreshold", 86400)
	if err != nil {
		return nil, err
	}
	r := &Reports{
		st:            st,
		cfg:           cfg,
		periodLimit:   time.Duration(limit) * time.Second,
		fastThreshold: fast,
		newTripLogic:  cfg.Bool("report.trip.newLogic", true),
		maxPositions:  maxPositions,
	}
	r.geocodeOnRequest = cfg.Bool("geocoder.enable", true) && cfg.String("geocoder.type", "locationiq") == "pluscodes" &&
		cfg.Bool("geocoder.onRequest", true)
	return r, nil
}

// Query is what every report takes: the user, the devices and groups asked for, and the
// period.
type Query struct {
	UserID    int64
	DeviceIDs []int64
	GroupIDs  []int64
	From, To  model.Time
}

// ErrPeriodLimit is checkPeriodLimit's IllegalArgumentException.
var ErrPeriodLimit = errors.New("Time period exceeds the limit")

func (r *Reports) checkPeriodLimit(q Query) error {
	if r.periodLimit > 0 && q.To.Time.Sub(q.From.Time) > r.periodLimit {
		return ErrPeriodLimit
	}
	return nil
}

// AccessibleDevices is DeviceUtil.getAccessibleDevices: the user's devices, those asked for
// and those in the groups asked for (and their subgroups). Java returns the filtered ones
// in a HashSet of identity-hashed devices, an order that changes between runs; here they
// keep the order of the user's devices.
func (r *Reports) AccessibleDevices(ctx context.Context, q Query) ([]model.Device, error) {
	devices, err := store.List[model.Device](ctx, r.st, store.ListOptions{
		Where: []store.Cond{store.Permission(store.KindUser, q.UserID, store.KindDevice)}})
	if err != nil {
		return nil, err
	}
	if len(q.DeviceIDs) == 0 && len(q.GroupIDs) == 0 {
		return devices, nil
	}
	groups, err := store.List[model.Group](ctx, r.st, store.ListOptions{
		Where: []store.Cond{store.Permission(store.KindUser, q.UserID, store.KindGroup)}})
	if err != nil {
		return nil, err
	}
	wanted := map[int64]bool{}
	for _, id := range q.DeviceIDs {
		wanted[id] = true
	}
	wantedGroups := map[int64]bool{}
	queue := slices.Clone(q.GroupIDs)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		wantedGroups[id] = true
		for _, g := range groups {
			if g.GroupID == id && g.GroupID > 0 {
				queue = append(queue, g.ID)
			}
		}
	}
	var out []model.Device
	for _, d := range devices {
		if wanted[d.ID] || d.GroupID > 0 && wantedGroups[d.GroupID] {
			out = append(out, d)
		}
	}
	return out, nil
}

// positions is PositionUtil.getPositionsStream: by fix time, at most limit (0: all).
func (r *Reports) positions(ctx context.Context, deviceID int64, from, to model.Time, limit int) ([]model.Position, error) {
	return r.st.Positions(ctx, deviceID, from, to, limit)
}

// edgePosition is PositionUtil.getEdgePosition: the first (or last) fix of the period.
func (r *Reports) edgePosition(ctx context.Context, deviceID int64, from, to model.Time, end bool) (*model.Position, error) {
	p, err := store.List[model.Position](ctx, r.st, store.ListOptions{
		Where:   []store.Cond{store.Equals("deviceId", deviceID), r.st.Between("fixTime", from, to)},
		OrderBy: "fixTime", Desc: end, Limit: 1})
	if err != nil || len(p) == 0 {
		return nil, err
	}
	return &p[0], nil
}

// position is a device's position by id (nil when missing).
func (r *Reports) position(ctx context.Context, deviceID, id int64) (*model.Position, error) {
	p, err := store.List[model.Position](ctx, r.st, store.ListOptions{
		Where: []store.Cond{store.Equals("deviceId", deviceID), store.Equals("id", id)}})
	if err != nil || len(p) == 0 {
		return nil, err
	}
	return &p[0], nil
}

// events is a device's events in the period by event time, of the given types (any when
// none).
func (r *Reports) events(ctx context.Context, deviceID int64, from, to model.Time, types ...string) ([]model.Event, error) {
	where := []store.Cond{store.Equals("deviceId", deviceID), r.st.Between("eventTime", from, to)}
	if len(types) > 0 {
		values := make([]any, len(types))
		for i, t := range types {
			values[i] = t
		}
		where = append(where, store.AnyOf("type", values...))
	}
	return store.List[model.Event](ctx, r.st, store.ListOptions{Where: where, OrderBy: "eventTime"})
}

// visible is ReportUtils.getObject != null: the user may see the object.
func (r *Reports) visible(ctx context.Context, userID int64, kind store.Kind, id int64, ids func(ctx context.Context, where ...store.Cond) ([]int64, error)) bool {
	found, err := ids(ctx, store.Equals("id", id), store.Permission(store.KindUser, userID, kind))
	return err == nil && len(found) > 0
}

// Route is RouteReportProvider.getObjects.
func (r *Reports) Route(ctx context.Context, q Query) ([]model.Position, error) {
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return nil, err
	}
	var out []model.Position
	for _, d := range devices {
		p, err := r.positions(ctx, d.ID, q.From, q.To, r.maxPositions)
		if err != nil {
			return nil, err
		}
		out = append(out, p...)
	}
	return out, nil
}

// Events is EventsReportProvider.getObjects: the types asked for (all when none or
// "allEvents"), alarms filtered by alarm type, and only events whose geofence and
// maintenance the user may see.
func (r *Reports) Events(ctx context.Context, q Query, types, alarms []string) ([]model.Event, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return nil, err
	}
	all := len(types) == 0 || slices.Contains(types, model.EventAllEvents)
	geofenceIDs := func(ctx context.Context, where ...store.Cond) ([]int64, error) {
		return store.IDs[model.Geofence](ctx, r.st, where...)
	}
	maintenanceIDs := func(ctx context.Context, where ...store.Cond) ([]int64, error) {
		return store.IDs[model.Maintenance](ctx, r.st, where...)
	}
	var out []model.Event
	for _, d := range devices {
		events, err := r.events(ctx, d.ID, q.From, q.To)
		if err != nil {
			return nil, err
		}
		for _, e := range events {
			if !all && !filterType(types, alarms, e) {
				continue
			}
			if e.GeofenceID > 0 && !r.visible(ctx, q.UserID, store.KindGeofence, e.GeofenceID, geofenceIDs) {
				continue
			}
			if e.MaintenanceID > 0 && !r.visible(ctx, q.UserID, store.KindMaintenance, e.MaintenanceID, maintenanceIDs) {
				continue
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func filterType(types, alarms []string, e model.Event) bool {
	if !slices.Contains(types, e.Type) {
		return false
	}
	if e.Type != model.EventAlarm || len(alarms) == 0 {
		return true
	}
	alarm := e.Attributes.JavaString(model.KeyAlarm)
	return alarm != nil && slices.Contains(alarms, *alarm)
}

// Geofences is GeofenceReportProvider.getObjects: from each enter to the next exit of the
// same geofence, sorted by start time.
func (r *Reports) Geofences(ctx context.Context, q Query, geofenceIDs []int64) ([]model.GeofenceReportItem, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return nil, err
	}
	out := []model.GeofenceReportItem{}
	for _, d := range devices {
		events, err := r.events(ctx, d.ID, q.From, q.To, model.EventGeofenceEnter, model.EventGeofenceExit)
		if err != nil {
			return nil, err
		}
		open := map[int64]model.Event{}
		for _, e := range events {
			if !slices.Contains(geofenceIDs, e.GeofenceID) {
				continue
			}
			switch e.Type {
			case model.EventGeofenceEnter:
				open[e.GeofenceID] = e
			case model.EventGeofenceExit:
				enter, ok := open[e.GeofenceID]
				delete(open, e.GeofenceID)
				if ok && !e.EventTime.Time.Before(enter.EventTime.Time) {
					name := d.Name
					out = append(out, model.GeofenceReportItem{
						ReportItem: model.ReportItem{DeviceID: d.ID, DeviceName: &name, StartTime: enter.EventTime, EndTime: e.EventTime},
						GeofenceID: e.GeofenceID,
					})
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b model.GeofenceReportItem) int { return a.StartTime.Time.Compare(b.StartTime.Time) })
	return out, nil
}

// Combined is CombinedReportProvider.getObjects.
func (r *Reports) Combined(ctx context.Context, q Query) ([]model.CombinedReportItem, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return nil, err
	}
	out := []model.CombinedReportItem{}
	for _, d := range devices {
		item := model.CombinedReportItem{DeviceID: d.ID, Route: [][2]float64{}, Events: []model.Event{}, Positions: []model.Position{}}
		events, err := r.events(ctx, d.ID, q.From, q.To)
		if err != nil {
			return nil, err
		}
		positionIDs := map[int64]bool{}
		for _, e := range events {
			positionIDs[e.PositionID] = true
			if e.PositionID > 0 && e.Type != model.EventDeviceMoving {
				item.Events = append(item.Events, e)
			}
		}
		positions, err := r.positions(ctx, d.ID, q.From, q.To, 0)
		if err != nil {
			return nil, err
		}
		for _, p := range positions {
			item.Route = append(item.Route, [2]float64{p.Longitude, p.Latitude})
			if positionIDs[p.ID] {
				item.Positions = append(item.Positions, p)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// Devices is DevicesReportProvider.getObjects: each device the user sees with its latest
// position.
func (r *Reports) Devices(ctx context.Context, userID int64, latest []model.Position) ([]model.DeviceReportItem, error) {
	devices, err := store.List[model.Device](ctx, r.st, store.ListOptions{
		Where: []store.Cond{store.Permission(store.KindUser, userID, store.KindDevice)}})
	if err != nil {
		return nil, err
	}
	byDevice := map[int64]*model.Position{}
	for i := range latest {
		byDevice[latest[i].DeviceID] = &latest[i]
	}
	out := make([]model.DeviceReportItem, len(devices))
	for i, d := range devices {
		out[i] = model.DeviceReportItem{Device: d, Position: byDevice[d.ID]}
	}
	return out, nil
}

// Summary is SummaryReportProvider.getObjects; daily splits the period at midnight in the
// user's time zone.
func (r *Reports) Summary(ctx context.Context, q Query, daily bool, zone *time.Location) ([]model.SummaryReportItem, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return nil, err
	}
	fast := int64(q.To.Time.Sub(q.From.Time)/time.Second) > r.fastThreshold
	out := []model.SummaryReportItem{}
	for _, d := range devices {
		from, to := q.From.Time.In(zone), q.To.Time.In(zone)
		var items []model.SummaryReportItem
		if daily {
			for startOfDay(from).Before(startOfDay(to)) {
				fromDay := startOfDay(from)
				next := plusDays(fromDay, 1)
				item, ok, err := r.summary(ctx, d, model.NewTime(from), model.NewTime(next), fast)
				if err != nil {
					return nil, err
				}
				if ok {
					items = append(items, item)
				}
				from = next
			}
		}
		item, ok, err := r.summary(ctx, d, model.NewTime(from), model.NewTime(to), fast)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, item)
		}
		out = append(out, items...)
	}
	return out, nil
}

// startOfDay is ZonedDateTime.truncatedTo(DAYS): local midnight, moved forward through a
// gap as Java resolves it.
func startOfDay(t time.Time) time.Time {
	return javatime.FromWall(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), t.Location())
}

// plusDays is ZonedDateTime.plusDays: the same local time n days later.
func plusDays(t time.Time, n int) time.Time {
	wall := time.Date(t.Year(), t.Month(), t.Day()+n, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	return javatime.FromWall(wall, t.Location())
}

// summary is calculateDeviceResult; ok is false without positions.
func (r *Reports) summary(ctx context.Context, d model.Device, from, to model.Time, fast bool) (model.SummaryReportItem, bool, error) {
	name := d.Name
	item := model.SummaryReportItem{ReportItem: model.ReportItem{DeviceID: d.ID, DeviceName: &name}}
	var first, last *model.Position
	if fast {
		var err error
		if first, err = r.edgePosition(ctx, d.ID, from, to, false); err != nil {
			return item, false, err
		}
		if last, err = r.edgePosition(ctx, d.ID, from, to, true); err != nil {
			return item, false, err
		}
	} else {
		positions, err := r.positions(ctx, d.ID, from, to, 0)
		if err != nil {
			return item, false, err
		}
		for i := range positions {
			p := &positions[i]
			if first == nil {
				first = p
			}
			if p.Speed > item.MaxSpeed {
				item.MaxSpeed = p.Speed
			}
			last = p
		}
	}
	if first == nil || last == nil {
		return item, false, nil
	}
	cfg, err := motion.LoadTripsConfig(ctx, attrs.New(r.st, r.cfg, d))
	if err != nil {
		return item, false, err
	}
	if item.Distance, err = distance(first, last, !cfg.IgnoreOdometer); err != nil {
		return item, false, err
	}
	if item.SpentFuel, err = fuel(first, last, &d); err != nil {
		return item, false, err
	}
	if first.Attributes.Has(model.KeyHours) && last.Attributes.Has(model.KeyHours) {
		if item.StartHours, err = first.Attributes.JavaLong(model.KeyHours); err != nil {
			return item, false, err
		}
		if item.EndHours, err = last.Attributes.JavaLong(model.KeyHours); err != nil {
			return item, false, err
		}
		if hours := item.EndHours - item.StartHours; hours > 0 {
			item.AverageSpeed = units.KnotsFromMps(item.Distance * 1000 / float64(hours))
		}
	}
	if item.StartOdometer, item.EndOdometer, err = odometers(first, last, cfg.IgnoreOdometer); err != nil {
		return item, false, err
	}
	item.StartTime, item.EndTime = first.FixTime, last.FixTime
	return item, !item.StartTime.IsZero() && !item.EndTime.IsZero(), nil
}

// distance is PositionUtil.calculateDistance.
func distance(first, last *model.Position, useOdometer bool) (float64, error) {
	fo, err := first.Attributes.JavaDouble(model.KeyOdometer, 0)
	if err != nil {
		return 0, err
	}
	lo, err := last.Attributes.JavaDouble(model.KeyOdometer, 0)
	if err != nil {
		return 0, err
	}
	if useOdometer && fo != 0 && lo != 0 {
		return lo - fo, nil
	}
	ft, err := first.Attributes.JavaDouble(model.KeyTotalDistance, 0)
	if err != nil {
		return 0, err
	}
	lt, err := last.Attributes.JavaDouble(model.KeyTotalDistance, 0)
	return lt - ft, err
}

// odometers are the start and end odometers: the device's when both are set and not
// ignored, else the total distances.
func odometers(first, last *model.Position, ignoreOdometer bool) (float64, float64, error) {
	key := model.KeyTotalDistance
	if !ignoreOdometer {
		fo, err := first.Attributes.JavaDouble(model.KeyOdometer, 0)
		if err != nil {
			return 0, 0, err
		}
		lo, err := last.Attributes.JavaDouble(model.KeyOdometer, 0)
		if err != nil {
			return 0, 0, err
		}
		if fo != 0 && lo != 0 {
			return fo, lo, nil
		}
	}
	a, err := first.Attributes.JavaDouble(key, 0)
	if err != nil {
		return 0, 0, err
	}
	b, err := last.Attributes.JavaDouble(key, 0)
	return a, b, err
}

// fuel is ReportUtils.calculateFuel.
func fuel(first, last *model.Position, d *model.Device) (float64, error) {
	both := func(k string) bool { return first.Attributes.Has(k) && last.Attributes.Has(k) }
	get := func(p *model.Position, k string) (float64, error) { return p.Attributes.JavaDouble(k, 0) }
	switch {
	case both(model.KeyFuelUsed):
		a, err := get(first, model.KeyFuelUsed)
		if err != nil {
			return 0, err
		}
		b, err := get(last, model.KeyFuelUsed)
		return b - a, err
	case both(model.KeyFuel):
		a, err := get(first, model.KeyFuel)
		if err != nil {
			return 0, err
		}
		b, err := get(last, model.KeyFuel)
		return a - b, err
	case both(model.KeyFuelLevel) && d.Attributes.Has("fuelCapacity"):
		a, err := get(first, model.KeyFuelLevel)
		if err != nil {
			return 0, err
		}
		b, err := get(last, model.KeyFuelLevel)
		if err != nil {
			return 0, err
		}
		capacity, err := d.Attributes.JavaDouble("fuelCapacity", 0)
		return (a - b) / 100 * capacity, err
	}
	return 0, nil
}

// address is a position's address, or its plus code when geocoding on request.
func (r *Reports) address(p *model.Position) *string {
	if p.Address != nil || !r.geocodeOnRequest {
		return p.Address
	}
	code := geo.PlusCode(p.Latitude, p.Longitude)
	return &code
}
