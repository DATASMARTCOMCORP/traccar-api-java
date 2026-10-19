package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/reports"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

const excelType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// reportRoutes are ReportResource's report endpoints. A path that produces both JSON and
// Excel answers by the Accept header, as Jersey does.
func (a *API) reportRoutes() {
	a.handleNegotiated("GET /api/reports/route", a.reportJSON("route", a.routeReport), a.reportExcel("route"))
	a.handleNegotiated("GET /api/reports/events", a.reportJSON("events", a.eventsReport), a.reportExcel("events"))
	a.handleNegotiated("GET /api/reports/summary", a.reportJSON("summary", a.summaryReport), a.reportExcel("summary"))
	a.handleNegotiated("GET /api/reports/trips", a.reportJSON("trips", a.tripsReport), a.reportExcel("trips"))
	a.handleNegotiated("GET /api/reports/stops", a.reportJSON("stops", a.stopsReport), a.reportExcel("stops"))
	a.handle("GET /api/reports/combined", authenticated, a.reportJSON("combined", a.combinedReport))
	a.handle("GET /api/reports/geofences", authenticated, a.reportJSON("geofences", a.geofencesReport))
	for _, name := range []string{"route", "events", "summary", "trips", "stops"} {
		a.handleExcel("GET /api/reports/"+name+"/{type}", a.reportExcel(name))
	}
	a.handleExcel("GET /api/reports/devices/{type}", a.reportExcel("devices"))
}

// handleNegotiated registers an endpoint producing JSON or Excel: Excel when the Accept
// header asks for it and not for JSON, JSON otherwise.
func (a *API) handleNegotiated(pattern string, json, excel handlerFunc) {
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		a.cors(w, r)
		accept := r.Header.Get("Accept")
		h := json
		if listsType(accept, excelType) && !listsType(accept, "application/json") {
			h = excel
		} else if !acceptsJSON(accept) {
			if !accepts(accept, "application", "vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
				a.fail(w, r, errStatus(http.StatusNotAcceptable, "Not Acceptable"))
				return
			}
			h = excel
		}
		req := &request{Request: r}
		if err := a.authenticate(req, authenticated); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := h(w, req); err != nil {
			a.fail(w, r, err)
		}
	})
}

// handleExcel registers an endpoint that only produces Excel.
func (a *API) handleExcel(pattern string, h handlerFunc) {
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		a.cors(w, r)
		if !accepts(r.Header.Get("Accept"), "application", "vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
			a.fail(w, r, errStatus(http.StatusNotAcceptable, "Not Acceptable"))
			return
		}
		if t := r.PathValue("type"); t != "xlsx" && t != "mail" {
			a.fail(w, r, errNoRoute)
			return
		}
		req := &request{Request: r}
		if err := a.authenticate(req, authenticated); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := h(w, req); err != nil {
			a.fail(w, r, err)
		}
	})
}

// listsType reports whether an Accept header names a media type itself, with q > 0.
func listsType(accept, typ string) bool {
	for _, part := range strings.Split(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || mt != typ {
			continue
		}
		if q, ok := params["q"]; ok && strings.Trim(q, "0.") == "" {
			continue
		}
		return true
	}
	return false
}

// reportQuery reads deviceId, groupId, from and to.
func (a *API) reportQuery(r *request) (reports.Query, error) {
	q := reports.Query{UserID: r.user.ID}
	var err error
	if q.DeviceIDs, err = qInt64s(r, "deviceId"); err != nil {
		return q, err
	}
	if q.GroupIDs, err = qInt64s(r, "groupId"); err != nil {
		return q, err
	}
	if q.From, err = qDate(r, "from"); err != nil {
		return q, err
	}
	q.To, err = qDate(r, "to")
	return q, err
}

// javaList is List.toString: "[1, 2]".
func javaList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// auditReport is LogAction.report. A missing date is Java's NullPointerException (400).
func (a *API) auditReport(ctx context.Context, r *request, name string, q reports.Query) error {
	if q.From.IsZero() || q.To.IsZero() {
		return errors.New("NullPointerException: missing date") // 400, unlike a bad date (404)
	}
	format := func(t model.Time) string { return t.Time.In(time.Local).Format("2006-01-02 15:04") }
	var attrs model.Attributes
	attrs.Set("type", name)
	attrs.Set("from", format(q.From))
	attrs.Set("to", format(q.To))
	attrs.Set("devices", javaList(q.DeviceIDs))
	attrs.Set("groups", javaList(q.GroupIDs))
	a.auditWith(ctx, r.Request, "report", r.user.ID, "", 0, attrs)
	return nil
}

// reportJSON wraps a report: the disableReports restriction, the audit row, then the
// report.
func (a *API) reportJSON(name string, report func(w http.ResponseWriter, r *request, q reports.Query) error) handlerFunc {
	return func(w http.ResponseWriter, r *request) error {
		if err := a.checkRestriction(r, func(x restrictions) bool { return x.disableReports }); err != nil {
			return err
		}
		q, err := a.reportQuery(r)
		if err != nil {
			return err
		}
		if err := a.auditReport(r.Context(), r, name, q); err != nil {
			return err
		}
		return report(w, r, q)
	}
}

// reportExcel is a report as a workbook (executeReport): the attachment, or with mail
// (the /mail path or mail=true) 204 at once and the workbook mailed to the user later.
// The audit row is written when the report runs, as Java's executor does.
func (a *API) reportExcel(name string) handlerFunc {
	return func(w http.ResponseWriter, r *request) error {
		if err := a.checkRestriction(r, func(x restrictions) bool { return x.disableReports }); err != nil {
			return labeled(err, excelType)
		}
		mail := r.PathValue("type") == "mail" || r.PathValue("type") == "" && qBool(r, "mail")
		var q reports.Query
		if name != "devices" {
			var err error
			if q, err = a.reportQuery(r); err != nil {
				return err
			}
		}
		x, err := a.excelOptions(r)
		if err != nil {
			return err
		}
		user := r.user
		build := func(ctx context.Context) ([]byte, error) {
			if name == "devices" {
				latest, err := a.latestPositions(r.Request, user.ID)
				if err != nil {
					return nil, err
				}
				return a.opts.Reports.DevicesExcel(ctx, user.ID, latest, x)
			}
			if err := a.auditReport(ctx, r, name, q); err != nil {
				return nil, err
			}
			switch name {
			case "route":
				return a.opts.Reports.RouteExcel(ctx, q, x)
			case "events":
				return a.opts.Reports.EventsExcel(ctx, q, x)
			case "summary":
				return a.opts.Reports.SummaryExcel(ctx, q, x)
			case "trips":
				return a.opts.Reports.TripsExcel(ctx, q, x)
			}
			return a.opts.Reports.StopsExcel(ctx, q, x)
		}
		if mail {
			go func() {
				ctx := context.WithoutCancel(r.Context())
				data, err := build(ctx)
				if err == nil && a.opts.MailReport != nil {
					err = a.opts.MailReport(user, data)
				}
				if err != nil {
					slog.Warn("Email report failed", "err", err)
				}
			}()
			w.WriteHeader(http.StatusNoContent)
			return nil
		}
		data, err := build(r.Context())
		if err != nil {
			return labeled(err, excelType)
		}
		w.Header().Set("Content-Type", excelType)
		w.Header().Set("Content-Disposition", "attachment; filename=report.xlsx")
		_, err = w.Write(data)
		return err
	}
}

// excelOptions are the user's preferences the templates use, and the events filter and
// summary flag.
func (a *API) excelOptions(r *request) (reports.Excel, error) {
	query := r.URL.Query()
	x := reports.Excel{Types: query["type"], Alarms: query["alarm"], Daily: qBool(r, "daily")}
	var err error
	if x.Zone, err = a.userZone(r); err != nil {
		return x, err
	}
	pref := func(key, def string) (string, error) {
		v, err := a.userPref(r, key)
		if err != nil || v == nil {
			return def, err
		}
		return *v, nil
	}
	if x.DistanceUnit, err = pref("distanceUnit", "km"); err != nil {
		return x, err
	}
	if x.SpeedUnit, err = pref("speedUnit", "kn"); err != nil {
		return x, err
	}
	x.VolumeUnit, err = pref("volumeUnit", "ltr")
	return x, err
}

func (a *API) routeReport(w http.ResponseWriter, r *request, q reports.Query) error {
	items, err := a.opts.Reports.Route(r.Context(), q)
	if err != nil {
		return err
	}
	return writeList(w, items)
}

func (a *API) eventsReport(w http.ResponseWriter, r *request, q reports.Query) error {
	query := r.URL.Query()
	items, err := a.opts.Reports.Events(r.Context(), q, query["type"], query["alarm"])
	if err != nil {
		return err
	}
	return writeList(w, items)
}

func (a *API) geofencesReport(w http.ResponseWriter, r *request, q reports.Query) error {
	ids, err := qInt64s(r, "geofenceId")
	if err != nil {
		return err
	}
	items, err := a.opts.Reports.Geofences(r.Context(), q, ids)
	if err != nil {
		return err
	}
	return writeList(w, items)
}

func (a *API) combinedReport(w http.ResponseWriter, r *request, q reports.Query) error {
	items, err := a.opts.Reports.Combined(r.Context(), q)
	if err != nil {
		return err
	}
	return writeList(w, items)
}

func (a *API) tripsReport(w http.ResponseWriter, r *request, q reports.Query) error {
	items, err := a.opts.Reports.Trips(r.Context(), q)
	if err != nil {
		return err
	}
	return writeList(w, items)
}

func (a *API) stopsReport(w http.ResponseWriter, r *request, q reports.Query) error {
	items, err := a.opts.Reports.Stops(r.Context(), q)
	if err != nil {
		return err
	}
	return writeList(w, items)
}

func (a *API) summaryReport(w http.ResponseWriter, r *request, q reports.Query) error {
	zone, err := a.userZone(r)
	if err != nil {
		return err
	}
	items, err := a.opts.Reports.Summary(r.Context(), q, qBool(r, "daily"), zone)
	if err != nil {
		return err
	}
	return writeList(w, items)
}

// userZone is UserUtil.getTimezone: the timezone preference, else the server's zone.
func (a *API) userZone(r *request) (*time.Location, error) {
	pref, err := a.userPref(r, "timezone")
	if err != nil || pref == nil {
		return time.Local, err
	}
	return javatime.TimeZone(*pref), nil
}

// userPref is UserUtil.lookupStringAttribute: the user's attribute, or the server's when
// it forces its settings or the user has none.
func (a *API) userPref(r *request, key string) (*string, error) {
	servers, err := store.All[model.Server](r.Context(), a.st)
	if err != nil {
		return nil, err
	}
	var sp *string
	force := false
	if len(servers) > 0 {
		sp, force = servers[0].Attributes.JavaString(key), servers[0].ForceSettings
	}
	up := r.user.Attributes.JavaString(key)
	if force && sp != nil || !force && up == nil {
		return sp, nil
	}
	return up, nil
}
