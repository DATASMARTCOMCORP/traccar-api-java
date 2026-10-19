package main

import (
	"fmt"
	"time"
)

// reportsSuite is Step 9's reports: the seed drives devices through trips, stops, gaps
// and a DST change (processed by Java: positions, totals and motion events are identical
// on both sides), then the scenarios ask for every report in JSON. Reports over several
// devices come in Java's HashSet order of devices, which changes between runs, so those
// compare as multisets.
func reportsSuite() suite {
	type fix struct {
		min      float64
		lat, lon float64
		speed    float64
		extra    string
	}
	drive := func(uniqueID string, start time.Time, fixes []fix) Step {
		var msgs []string
		for _, f := range fixes {
			at := start.Add(time.Duration(f.min * float64(time.Minute))).Format("2006-01-02T15:04:05Z")
			msgs = append(msgs, fmt.Sprintf("/?id=%s&lat=%v&lon=%v&timestamp=%s&speed=%v%s", uniqueID, f.lat, f.lon, at, f.speed, f.extra))
		}
		return osmand(msgs...)
	}
	// A line of fixes from one point to another, every two minutes.
	line := func(from, to fix, n int, extra string) []fix {
		var out []fix
		for i := 0; i <= n; i++ {
			k := float64(i) / float64(n)
			out = append(out, fix{from.min + k*(to.min-from.min), from.lat + k*(to.lat-from.lat), from.lon + k*(to.lon-from.lon), from.speed, extra})
		}
		return out
	}
	day1 := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	dst := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)

	var carFixes []fix
	carFixes = append(carFixes, fix{0, -23.55, -46.63, 0, "&ignition=false&fuel=50"}, fix{5, -23.55, -46.63, 0, "&ignition=true&fuel=50&driverUniqueId=D1"})
	carFixes = append(carFixes, line(fix{7, -23.55, -46.63, 30, ""}, fix{27, -23.48, -46.55, 30, ""}, 10, "&ignition=true&fuel=45")...)
	carFixes = append(carFixes, fix{30, -23.48, -46.55, 0, "&ignition=false&fuel=44"}, fix{45, -23.48, -46.55, 0, "&ignition=false"}, fix{70, -23.48, -46.5501, 0, "&ignition=false"})
	carFixes = append(carFixes, line(fix{72, -23.48, -46.55, 25, ""}, fix{100, -23.40, -46.60, 25, ""}, 14, "&ignition=true&fuel=40&driverUniqueId=D2")...)
	carFixes = append(carFixes, fix{104, -23.40, -46.60, 0, "&ignition=false&fuel=39"}, fix{130, -23.40, -46.60, 0, "&ignition=false"})

	var car2 []fix
	car2 = append(car2, fix{0, -23.40, -46.60, 0, "&ignition=false"}, fix{10, -23.4001, -46.6001, 2, "&ignition=true"}, fix{20, -23.40, -46.60, 0, "&ignition=false"})
	// A jump after a gap longer than report.trip.stopGap.
	car2 = append(car2, fix{150, -23.30, -46.50, 0, "&ignition=false"}, fix{152, -23.30, -46.50, 0, ""})
	car2 = append(car2, line(fix{160, -23.30, -46.50, 40, ""}, fix{180, -23.20, -46.40, 40, ""}, 10, "&ignition=true")...)

	var truck []fix
	truck = append(truck, fix{0, -22.90, -43.20, 0, "&ignition=false&odometer=100000&fuelUsed=10&fuelLevel=80"})
	truck = append(truck, line(fix{4, -22.90, -43.20, 50, ""}, fix{40, -22.80, -43.00, 50, ""}, 18, "&ignition=true&odometer=110000&fuelUsed=14&fuelLevel=70")...)
	truck = append(truck, fix{44, -22.80, -43.00, 0, "&ignition=false&odometer=118000&fuelUsed=15&fuelLevel=68"}, fix{60, -22.80, -43.00, 0, "&ignition=false&odometer=118000"})

	var van []fix
	for h := 0; h < 60; h += 2 {
		lat := -23.6 + float64(h%10)*0.01
		speed, extra := 0.0, "&ignition=false"
		if h%10 < 4 {
			speed, extra = 20, "&ignition=true"
		}
		van = append(van, fix{float64(h * 60), lat, -46.7, speed, extra})
	}

	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`).save("admin", "id"),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/users", `{"name":"Partial","email":"partial@example.com","password":"secret","attributes":{"timezone":"America/New_York"}}`).save("partial", "id"),
		postJSON("admin", "/api/users", `{"name":"Blocked","email":"blocked@example.com","password":"secret","disableReports":true}`).save("blocked", "id"),
		postJSON("admin", "/api/groups", `{"name":"Fleet"}`).save("fleet", "id"),
		postJSON("admin", "/api/groups", `{"name":"Heavy","groupId":${fleet}}`).save("heavy", "id"),
		postJSON("admin", "/api/devices", `{"name":"Car","uniqueId":"400001","groupId":${fleet}}`).save("car", "id"),
		postJSON("admin", "/api/devices", `{"name":"Truck","uniqueId":"400002","groupId":${heavy},"attributes":{"fuelCapacity":300}}`).save("truck", "id"),
		postJSON("admin", "/api/devices", `{"name":"Van","uniqueId":"400003","attributes":{"report.ignoreOdometer":true}}`).save("van", "id"),
		postJSON("admin", "/api/drivers", `{"name":"Driver One","uniqueId":"D1"}`),
		postJSON("admin", "/api/geofences", `{"name":"Depot","area":"CIRCLE (-23.48 -46.55, 500)"}`).save("depot", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Hidden","area":"CIRCLE (-23.40 -46.60, 500)"}`).save("hidden", "id"),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${depot}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${hidden}}`),
		postJSON("admin", "/api/permissions", `{"userId":${partial},"deviceId":${car}}`),
		postJSON("admin", "/api/permissions", `{"userId":${partial},"deviceId":${van}}`),
		postJSON("admin", "/api/permissions", `{"userId":${partial},"geofenceId":${depot}}`),
		postJSON("admin", "/api/permissions", `{"userId":${blocked},"deviceId":${car}}`),
		drive("400001", day1, carFixes),
		drive("400001", day2, car2),
		drive("400002", day1, truck),
		drive("400003", dst, van),
	}
	config := map[string]string{
		"server.buffering.threshold": "0",
		"report.periodLimit":         "2592000", // 30 days
	}

	var steps []Step
	login := func(user string) Step { return login(user, user+"@example.com", "secret") }
	steps = append(steps, login("admin"), login("partial"), login("blocked"))
	q := func(actor, path string, multi bool) Step {
		s := get(actor, path).header("Accept", "application/json")
		if multi {
			s = s.unordered("$")
		}
		return s
	}
	periods := []struct {
		from, to string
	}{
		{"2026-10-05T00:00:00Z", "2026-10-05T23:59:59Z"}, // one day: slow detection
		{"2026-10-05T10:20:00Z", "2026-10-05T11:30:00Z"}, // starts mid-trip
		{"2026-10-01T00:00:00Z", "2026-10-10T00:00:00Z"}, // over report.fastThreshold: stored events
	}
	for _, p := range periods {
		span := "from=" + p.from + "&to=" + p.to
		for _, kind := range []string{"route", "events", "summary", "trips", "stops", "combined"} {
			steps = append(steps,
				q("admin", "/api/reports/"+kind+"?deviceId=${car}&"+span, false),
				q("admin", "/api/reports/"+kind+"?deviceId=${truck}&"+span, false),
				q("admin", "/api/reports/"+kind+"?groupId=${fleet}&"+span, true),
				q("admin", "/api/reports/"+kind+"?deviceId=${car}&deviceId=${van}&"+span, true),
			)
		}
		steps = append(steps,
			q("admin", "/api/reports/geofences?deviceId=${car}&geofenceId=${depot}&geofenceId=${hidden}&"+span, false),
			q("admin", "/api/reports/summary?deviceId=${car}&daily=true&"+span, false),
		)
	}
	steps = append(steps,
		// All devices; another user's view (geofence visibility, time zone).
		q("admin", "/api/reports/trips?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z", true),
		q("partial", "/api/reports/events?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z", true),
		q("partial", "/api/reports/summary?deviceId=${van}&daily=true&from=2026-10-31T00:00:00Z&to=2026-11-03T00:00:00Z", false),
		q("admin", "/api/reports/summary?deviceId=${van}&daily=true&from=2026-10-31T00:00:00Z&to=2026-11-03T00:00:00Z", false),
		q("partial", "/api/reports/route?deviceId=${truck}&from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z", false),
		// Event types and alarms.
		q("admin", "/api/reports/events?deviceId=${car}&type=deviceMoving&type=geofenceEnter&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z", false),
		q("admin", "/api/reports/events?deviceId=${car}&type=allEvents&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z", false),
		q("admin", "/api/reports/events?deviceId=${car}&type=alarm&alarm=sos&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z", false),
		// Errors: no reports for the user, a missing date, a period over report.periodLimit.
		q("blocked", "/api/reports/trips?deviceId=${car}&from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z", false),
		q("admin", "/api/reports/trips?deviceId=${car}&from=2026-10-05T00:00:00Z", false),
		q("admin", "/api/reports/route?deviceId=${car}&from=bad&to=2026-10-06T00:00:00Z", false),
		q("admin", "/api/reports/trips?deviceId=${car}&from=2026-08-01T00:00:00Z&to=2026-10-06T00:00:00Z", false),
		q("admin", "/api/reports/route?deviceId=${car}&from=2026-08-01T00:00:00Z&to=2026-10-06T00:00:00Z", false),
		// Accept headers: none, any.
		get("admin", "/api/reports/summary?deviceId=${car}&from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z"),
		get("admin", "/api/reports/summary?deviceId=${car}&from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z").header("Accept", "*/*"),
		get("admin", "/api/audit?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
	)
	// Excel: the workbooks' contents (sheets, cells, styles, links) are compared.
	xl := func(actor, path string) Step { return get(actor, path).header("Accept", excelType).xlsx() }
	for _, kind := range []string{"route", "events", "summary", "trips", "stops"} {
		steps = append(steps,
			xl("admin", "/api/reports/"+kind+"?deviceId=${car}&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"),
			xl("admin", "/api/reports/"+kind+"/xlsx?groupId=${fleet}&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z").unordered("$"),
			xl("partial", "/api/reports/"+kind+"/xlsx?deviceId=${car}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
		)
	}
	steps = append(steps,
		xl("admin", "/api/reports/summary/xlsx?deviceId=${car}&daily=true&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"),
		xl("admin", "/api/reports/devices/xlsx"),
		xl("partial", "/api/reports/devices/xlsx"),
		xl("admin", "/api/reports/route?deviceId=${car}&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z&mail=true"),
		xl("admin", "/api/reports/devices/mail"),
		xl("admin", "/api/reports/events/xlsx?deviceId=${car}&from=2026-08-01T00:00:00Z&to=2026-10-06T00:00:00Z"),
		xl("admin", "/api/reports/trips/xlsx?deviceId=${car}&from=2026-10-05T00:00:00Z"),
		xl("admin", "/api/reports/route/csv?deviceId=${car}&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"),
	)
	// Position exports (PositionResource).
	const period = "&from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"
	steps = append(steps,
		get("admin", "/api/positions/kml?deviceId=${car}"+period),
		get("admin", "/api/positions/kmz?deviceId=${truck}"+period).binary(),
		get("admin", "/api/positions/gpx?deviceId=${car}"+period),
		get("admin", "/api/positions/csv?deviceId=${car}"+period).csv(),
		get("admin", "/api/positions/csv?deviceId=${truck}"+period).csv(),
		get("partial", "/api/positions/csv?deviceId=${car}"+period).csv(),
		get("admin", "/api/positions/kml?deviceId=${car}&geofenceId=${depot}"+period),
		get("admin", "/api/positions/gpx?deviceId=${car}&geofenceId=${depot}"+period),
		get("admin", "/api/positions/csv?deviceId=${car}&geofenceId=${depot}"+period).csv(),
		get("partial", "/api/positions/gpx?deviceId=${truck}"+period),
		get("blocked", "/api/positions/kml?deviceId=${car}"+period),
		get("admin", "/api/positions/kml?deviceId=${car}"),
		get("admin", "/api/positions/gpx?deviceId=${car}"),
		get("admin", "/api/positions/csv?deviceId=${car}").csv(),
		get("admin", "/api/positions/gpx?deviceId=999999"+period),
		get("admin", "/api/positions/kml?deviceId=${car}"+period).header("Accept", "application/xml"),
		get("admin", "/api/positions/csv?deviceId=${car}"+period).header("Accept", "application/json"),
		get("admin", "/api/positions/gpx?deviceId=${car}"+period).header("Accept", "application/gpx+xml"),
		get("admin", "/api/positions/abc?deviceId=${car}"+period),
		get("anon", "/api/positions/kml?deviceId=${car}"+period),
	)
	return suite{seed: seed, strictSeed: true, config: config, scenarios: []Scenario{
		{Name: "reports-json", Steps: steps},
	}}
}

// reportsLegacySuite is reportsSuite with report.trip.newLogic off: the legacy
// MotionProcessor detects motion events in processing and trips and stops in reports.
func reportsLegacySuite() suite {
	s := reportsSuite()
	s.config["report.trip.newLogic"] = "false"
	s.scenarios[0].Name = "reports-legacy-json"
	return s
}
