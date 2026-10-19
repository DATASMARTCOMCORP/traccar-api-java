package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/xlsx"
)

// Excel is what the Excel reports take besides the query.
type Excel struct {
	// Units are the user's distanceUnit, speedUnit and volumeUnit (UserUtil).
	DistanceUnit, SpeedUnit, VolumeUnit string
	Zone                                *time.Location // UserUtil.getTimezone
	Types, Alarms                       []string       // events: the types and alarms asked for
	Daily                               bool           // summary
}

// context is ReportUtils.initializeContext with the period.
func (r *Reports) context(q Query, x Excel) map[string]any {
	return map[string]any{
		"distanceUnit":  x.DistanceUnit,
		"speedUnit":     x.SpeedUnit,
		"volumeUnit":    x.VolumeUnit,
		"webUrl":        nil,
		"dateTool":      xlsx.DateTool,
		"util":          xlsx.Util,
		"timezone":      x.Zone,
		"locale":        "en",
		"bracketsRegex": `[\{\}"]`,
		"from":          q.From.Time,
		"to":            q.To.Time,
	}
}

func (r *Reports) template(name string) string {
	return filepath.Join(r.cfg.String("templates.root", "templates"), "export", name+".xlsx")
}

// section is a DeviceReportSection: the device's name, its group's name and its rows.
func (r *Reports) section(ctx context.Context, d model.Device, objects []any) (xlsx.Bean, error) {
	s := xlsx.Bean{"deviceName": d.Name, "groupName": nil, "objects": objects}
	if d.GroupID > 0 {
		g, err := store.Get[model.Group](ctx, r.st, d.GroupID)
		if err == nil {
			s["groupName"] = g.Name
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return s, nil
}

func beans[T json.Marshaler](items []T) ([]any, error) {
	out := make([]any, len(items))
	for i, it := range items {
		b, err := xlsx.BeanOf(it)
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

// safeSheetName is POI's WorkbookUtil.createSafeSheetName.
func safeSheetName(name string) string {
	if name == "" {
		return " empty"
	}
	u := []rune(name)
	if len(u) > 31 {
		u = u[:31]
	}
	for i, c := range u {
		switch c {
		case '\n', '\r', '\t', 0, 3, ':', '/', '\\', '?', '*', ']', '[':
			u[i] = ' '
		case '\'':
			if i == 0 || i == len(u)-1 {
				u[i] = ' '
			}
		}
	}
	return string(u)
}

// sheets fills a per-device template: one sheet per device, named after it.
func (r *Reports) sheets(ctx context.Context, name string, q Query, x Excel, unique bool, extra map[string]any,
	rows func(d model.Device) ([]any, error)) ([]byte, error) {
	devices, err := r.AccessibleDevices(ctx, q)
	if err != nil {
		return nil, err
	}
	var sections, names []any
	counts := map[string]int{}
	for _, d := range devices {
		objects, err := rows(d)
		if err != nil {
			return nil, err
		}
		s, err := r.section(ctx, d, objects)
		if err != nil {
			return nil, err
		}
		sheet := d.Name
		if unique { // RouteReportProvider.getUniqueSheetName
			counts[sheet]++
			if n := counts[sheet]; n > 1 {
				sheet = fmt.Sprintf("%s-%d", sheet, n)
			}
		}
		names = append(names, safeSheetName(sheet))
		sections = append(sections, s)
	}
	c := r.context(q, x)
	c["devices"] = sections
	c["sheetNames"] = names
	for k, v := range extra {
		c[k] = v
	}
	return xlsx.Render(r.template(name), c)
}

// RouteExcel is RouteReportProvider.getExcel.
func (r *Reports) RouteExcel(ctx context.Context, q Query, x Excel) ([]byte, error) {
	return r.sheets(ctx, "route", q, x, true, nil, func(d model.Device) ([]any, error) {
		p, err := r.positions(ctx, d.ID, q.From, q.To, r.maxPositions)
		if err != nil {
			return nil, err
		}
		return beans(p)
	})
}

// EventsExcel is EventsReportProvider.getExcel: unlike the JSON report, an event with a
// geofence is checked only against its geofence, and one with a maintenance against that.
func (r *Reports) EventsExcel(ctx context.Context, q Query, x Excel) ([]byte, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	all := len(x.Types) == 0 || slices.Contains(x.Types, model.EventAllEvents)
	geofenceNames, maintenanceNames, positions := map[int64]any{}, map[int64]any{}, map[int64]any{}
	extra := map[string]any{"geofenceNames": geofenceNames, "maintenanceNames": maintenanceNames, "positions": positions}
	return r.sheets(ctx, "events", q, x, false, extra, func(d model.Device) ([]any, error) {
		events, err := r.events(ctx, d.ID, q.From, q.To)
		if err != nil {
			return nil, err
		}
		var kept []model.Event
		for _, e := range events {
			if !all && !filterType(x.Types, x.Alarms, e) {
				continue
			}
			switch {
			case e.GeofenceID != 0:
				g, ok, err := visibleObject[model.Geofence](ctx, r, q.UserID, store.KindGeofence, e.GeofenceID)
				if err != nil {
					return nil, err
				}
				if ok {
					geofenceNames[e.GeofenceID] = g.Name
					kept = append(kept, e)
				}
			case e.MaintenanceID != 0:
				m, ok, err := visibleObject[model.Maintenance](ctx, r, q.UserID, store.KindMaintenance, e.MaintenanceID)
				if err != nil {
					return nil, err
				}
				if ok {
					maintenanceNames[e.MaintenanceID] = m.Name
					kept = append(kept, e)
				}
			default:
				kept = append(kept, e)
			}
		}
		for _, e := range kept {
			if e.PositionID > 0 {
				p, err := r.position(ctx, d.ID, e.PositionID)
				if err != nil {
					return nil, err
				}
				positions[e.PositionID] = nil
				if p != nil {
					if positions[e.PositionID], err = xlsx.BeanOf(p); err != nil {
						return nil, err
					}
				}
			}
		}
		return beans(kept)
	})
}

// visibleObject is ReportUtils.getObject: the object when the user may see it.
func visibleObject[T store.Entity](ctx context.Context, r *Reports, userID int64, kind store.Kind, id int64) (T, bool, error) {
	var zero T
	list, err := store.List[T](ctx, r.st, store.ListOptions{
		Where: []store.Cond{store.Equals("id", id), store.Permission(store.KindUser, userID, kind)}})
	if err != nil || len(list) == 0 {
		return zero, false, err
	}
	return list[0], true, nil
}

// TripsExcel is TripsReportProvider.getExcel.
func (r *Reports) TripsExcel(ctx context.Context, q Query, x Excel) ([]byte, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	return r.sheets(ctx, "trips", q, x, false, nil, func(d model.Device) ([]any, error) {
		var items []model.TripReportItem
		err := r.detect(ctx, d, q.From, q.To, true, func(it any) { items = append(items, it.(model.TripReportItem)) })
		if err != nil {
			return nil, err
		}
		return beans(items)
	})
}

// StopsExcel is StopsReportProvider.getExcel.
func (r *Reports) StopsExcel(ctx context.Context, q Query, x Excel) ([]byte, error) {
	if err := r.checkPeriodLimit(q); err != nil {
		return nil, err
	}
	return r.sheets(ctx, "stops", q, x, false, nil, func(d model.Device) ([]any, error) {
		var items []model.StopReportItem
		err := r.detect(ctx, d, q.From, q.To, false, func(it any) { items = append(items, it.(model.StopReportItem)) })
		if err != nil {
			return nil, err
		}
		return beans(items)
	})
}

// SummaryExcel is SummaryReportProvider.getExcel.
func (r *Reports) SummaryExcel(ctx context.Context, q Query, x Excel) ([]byte, error) {
	items, err := r.Summary(ctx, q, x.Daily, x.Zone)
	if err != nil {
		return nil, err
	}
	summaries, err := beans(items)
	if err != nil {
		return nil, err
	}
	c := r.context(q, x)
	c["summaries"] = summaries
	return xlsx.Render(r.template("summary"), c)
}

// DevicesExcel is DevicesReportProvider.getExcel (no period: from and to are null).
func (r *Reports) DevicesExcel(ctx context.Context, userID int64, latest []model.Position, x Excel) ([]byte, error) {
	items, err := r.Devices(ctx, userID, latest)
	if err != nil {
		return nil, err
	}
	list, err := beans(items)
	if err != nil {
		return nil, err
	}
	c := r.context(Query{}, x)
	c["from"], c["to"] = nil, nil
	c["items"] = list
	return xlsx.Render(r.template("devices"), c)
}
