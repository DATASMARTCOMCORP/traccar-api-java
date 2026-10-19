package model

// Report items (org.traccar.reports.model), written as Jackson writes them: BaseReportItem's
// properties first, then the subclass's, then getter-only ones.

// ReportItem is BaseReportItem.
type ReportItem struct {
	DeviceID      int64
	DeviceName    *string
	Distance      float64
	AverageSpeed  float64
	MaxSpeed      float64
	SpentFuel     float64
	StartOdometer float64
	EndOdometer   float64
	StartTime     Time
	EndTime       Time
}

func (r *ReportItem) write(o *object) {
	o.int("deviceId", r.DeviceID)
	o.nullStr("deviceName", r.DeviceName)
	o.double("distance", r.Distance)
	o.double("averageSpeed", r.AverageSpeed)
	o.double("maxSpeed", r.MaxSpeed)
	o.double("spentFuel", r.SpentFuel)
	o.double("startOdometer", r.StartOdometer)
	o.double("endOdometer", r.EndOdometer)
	o.time("startTime", r.StartTime)
	o.time("endTime", r.EndTime)
}

// TripReportItem is a trip.
type TripReportItem struct {
	ReportItem
	StartPositionID int64
	EndPositionID   int64
	StartLat        float64
	StartLon        float64
	EndLat          float64
	EndLon          float64
	StartAddress    *string
	EndAddress      *string
	Duration        int64
	DriverUniqueID  *string
	DriverName      *string
}

func (t TripReportItem) MarshalJSON() ([]byte, error) {
	o := newObject()
	t.write(o)
	o.int("startPositionId", t.StartPositionID)
	o.int("endPositionId", t.EndPositionID)
	o.double("startLat", t.StartLat)
	o.double("startLon", t.StartLon)
	o.double("endLat", t.EndLat)
	o.double("endLon", t.EndLon)
	o.nullStr("startAddress", t.StartAddress)
	o.nullStr("endAddress", t.EndAddress)
	o.int("duration", t.Duration)
	o.nullStr("driverUniqueId", t.DriverUniqueID)
	o.nullStr("driverName", t.DriverName)
	return o.end()
}

// StopReportItem is a stop.
type StopReportItem struct {
	ReportItem
	PositionID  int64
	Latitude    float64
	Longitude   float64
	Address     *string
	Duration    int64
	EngineHours int64 // milliseconds
}

func (s StopReportItem) MarshalJSON() ([]byte, error) {
	o := newObject()
	s.write(o)
	o.int("positionId", s.PositionID)
	o.double("latitude", s.Latitude)
	o.double("longitude", s.Longitude)
	o.nullStr("address", s.Address)
	o.int("duration", s.Duration)
	o.int("engineHours", s.EngineHours)
	return o.end()
}

// SummaryReportItem is a summary; engineHours is endHours - startHours.
type SummaryReportItem struct {
	ReportItem
	StartHours int64 // milliseconds
	EndHours   int64
}

func (s SummaryReportItem) MarshalJSON() ([]byte, error) {
	o := newObject()
	s.write(o)
	o.int("startHours", s.StartHours)
	o.int("endHours", s.EndHours)
	o.int("engineHours", s.EndHours-s.StartHours)
	return o.end()
}

// GeofenceReportItem is a stay in a geofence.
type GeofenceReportItem struct {
	ReportItem
	GeofenceID int64
}

func (g GeofenceReportItem) MarshalJSON() ([]byte, error) {
	o := newObject()
	g.write(o)
	o.int("geofenceId", g.GeofenceID)
	return o.end()
}

// CombinedReportItem is a device's route, events and the events' positions.
type CombinedReportItem struct {
	DeviceID  int64
	Route     [][2]float64 // longitude, latitude
	Events    []Event
	Positions []Position
}

func (c CombinedReportItem) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.int("deviceId", c.DeviceID)
	route := []byte{'['}
	for i, p := range c.Route {
		if i > 0 {
			route = append(route, ',')
		}
		route = append(route, '[')
		route = appendDouble(route, p[0])
		route = append(route, ',')
		route = appendDouble(route, p[1])
		route = append(route, ']')
	}
	o.raw("route", append(route, ']'))
	if err := o.list("events", len(c.Events), func(i int) ([]byte, error) { return c.Events[i].MarshalJSON() }); err != nil {
		return nil, err
	}
	if err := o.list("positions", len(c.Positions), func(i int) ([]byte, error) { return c.Positions[i].MarshalJSON() }); err != nil {
		return nil, err
	}
	return o.end()
}

// DeviceReportItem is a device with its latest position (null when none).
type DeviceReportItem struct {
	Device   Device
	Position *Position
}

func (d DeviceReportItem) MarshalJSON() ([]byte, error) {
	o := newObject()
	data, err := d.Device.MarshalJSON()
	if err != nil {
		return nil, err
	}
	o.raw("device", data)
	if d.Position == nil {
		o.null("position")
	} else {
		data, err := d.Position.MarshalJSON()
		if err != nil {
			return nil, err
		}
		o.raw("position", data)
	}
	return o.end()
}

// list writes a JSON array of n items.
func (o *object) list(name string, n int, item func(int) ([]byte, error)) error {
	b := []byte{'['}
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		data, err := item(i)
		if err != nil {
			return err
		}
		b = append(b, data...)
	}
	o.raw(name, append(b, ']'))
	return nil
}
