package main

import (
	"fmt"
	"time"
)

// ingestSuite is Step 6: OsmAnd traffic through the whole position pipeline. The seed
// (run through Java) creates devices whose attributes and links switch processing
// features on; the scenarios replay OsmAnd messages in every form the decoder accepts or
// rejects, and the database comparison after each scenario checks tc_positions,
// tc_devices and tc_events (and every other table).
func ingestSuite() suite {
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`).save("admin", "id"),
		login("admin", "admin@example.com", "secret"),
		// Group attributes reach devices through the group chain.
		postJSON("admin", "/api/groups", `{"name":"Fleet","attributes":{"speedLimit":30.0,"report.trip.minDuration":120}}`).save("fleet", "id"),
		postJSON("admin", "/api/groups", `{"name":"Sub","groupId":${fleet}}`).save("sub", "id"),
		postJSON("admin", "/api/devices", `{"name":"Car","uniqueId":"100001","groupId":${sub},
			"attributes":{"fuelDropThreshold":5,"fuelIncreaseThreshold":"10"}}`).save("car", "id"),
		postJSON("admin", "/api/devices", `{"name":"Filtered","uniqueId":"100002","attributes":{"filter.duplicate":true,
			"filter.zero":"true","filter.static":"true","filter.skipAttributes":"alarm","filter.minPeriod":30,
			"filter.maxSpeed":"200","filter.accuracy":50}}`).save("filtered", "id"),
		postJSON("admin", "/api/devices", `{"name":"Bad zones","uniqueId":"100003"}`).save("badzones", "id"),
		postJSON("admin", "/api/devices", `{"name":"Buddy","uniqueId":"100004",
			"attributes":{"proximityEnterDistance":300,"proximityExitDistance":"500","unaccompaniedDistance":1000}}`).save("buddy", "id"),
		postJSON("admin", "/api/devices", `{"name":"Off","uniqueId":"100005","disabled":true}`),
		postJSON("admin", "/api/devices", `{"name":"Clock","uniqueId":"100006","attributes":{"time.override":"serverTime"}}`).save("clock", "id"),
		postJSON("admin", "/api/devices", `{"name":"Formats","uniqueId":"100007"}`).save("formats", "id"),
		postJSON("admin", "/api/devices", `{"name":"Fresh","uniqueId":"100008"}`).save("fresh", "id"),
		postJSON("admin", "/api/devices", `{"name":"Hours","uniqueId":"100009","attributes":{"event.motion.speedThreshold":"2.5",
			"processing.copyAttributes":"door fuel"}}`).save("hours", "id"),

		// Geofences: a circle around the start, a polygon and a polyline on the route,
		// one with a floor, and two Java can't parse (production has both kinds).
		postJSON("admin", "/api/geofences", `{"name":"Home","area":"CIRCLE (-23.5505 -46.6333, 300)"}`).save("home", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Zone","area":"POLYGON ((-23.545 -46.625, -23.545 -46.615, -23.535 -46.615, -23.535 -46.625, -23.545 -46.625))"}`).save("zone", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Road","area":"LINESTRING (-23.530 -46.610, -23.520 -46.600, -23.510 -46.590)","attributes":{"polylineDistance":50,"speedLimit":40.5}}`).save("road", "id"),
		postJSON("admin", "/api/geofences", `{"name":"High","area":"CIRCLE (-23.5505 -46.6333, 5000)","attributes":{"floor":"750"}}`).save("high", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Multi","area":"MULTILINESTRING ((-23.5 -46.6, -23.4 -46.5))"}`).save("multi", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Comma","area":"CIRCLE(-23,5505 -46,6333, 300)"}`).save("comma", "id"),
		postJSON("admin", "/api/permissions", `{"groupId":${fleet},"geofenceId":${home}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${zone}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${road}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${high}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${formats},"geofenceId":${home}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${badzones},"geofenceId":${home}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${badzones},"geofenceId":${multi}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${hours},"geofenceId":${comma}}`),

		// Calendars as traccar-web makes them (TZID times, a weekly rule), an hourly rule
		// in January (ical4j's own America/Sao_Paulo rules still switch to -02:00 then),
		// and a single event.
		postJSON("admin", "/api/calendars", `{"name":"Weekdays","data":"QkVHSU46VkNBTEVOREFSClZFUlNJT046Mi4wClBST0RJRDotLy9UcmFjY2FyLy9OT05TR01MIFRyYWNjYXIvL0VOCkJFR0lOOlZFVkVOVApVSUQ6MDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwCkRUU1RBUlQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI2MTAwMVQwODAwMDAKRFRFTkQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI2MTAwMVQxODAwMDAKUlJVTEU6RlJFUT1XRUVLTFk7QllEQVk9TU8sVFUsV0UsVEgsRlIKU1VNTUFSWTpFdmVudApFTkQ6VkVWRU5UCkVORDpWQ0FMRU5EQVI="}`).save("weekdays", "id"),
		postJSON("admin", "/api/calendars", `{"name":"Every two hours","data":"QkVHSU46VkNBTEVOREFSClZFUlNJT046Mi4wClBST0RJRDotLy9UcmFjY2FyLy9OT05TR01MIFRyYWNjYXIvL0VOCkJFR0lOOlZFVkVOVApVSUQ6MDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAxCkRUU1RBUlQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI1MTIwMVQwODAwMDAKRFRFTkQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI1MTIwMVQwOTAwMDAKUlJVTEU6RlJFUT1IT1VSTFk7SU5URVJWQUw9MgpTVU1NQVJZOkV2ZW50CkVORDpWRVZFTlQKRU5EOlZDQUxFTkRBUg=="}`).save("hourly", "id"),
		postJSON("admin", "/api/calendars", `{"name":"Once","data":"QkVHSU46VkNBTEVOREFSClZFUlNJT046Mi4wClBST0RJRDotLy9UcmFjY2FyLy9OT05TR01MIFRyYWNjYXIvL0VOCkJFR0lOOlZFVkVOVApVSUQ6MDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAyCkRUU1RBUlQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI2MTAxM1QwODAwMDAKRFRFTkQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI2MTAxM1QwOTAwMDAKU1VNTUFSWTpFdmVudApFTkQ6VkVWRU5UCkVORDpWQ0FMRU5EQVI="}`).save("once", "id"),
		postJSON("admin", "/api/devices", `{"name":"Scheduled","uniqueId":"100010","calendarId":${weekdays}}`).save("scheduled", "id"),
		postJSON("admin", "/api/devices", `{"name":"Hourly","uniqueId":"100011","calendarId":${hourly}}`).save("hourlyDevice", "id"),
		postJSON("admin", "/api/devices", `{"name":"Once","uniqueId":"100012","calendarId":${once}}`).save("onceDevice", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Office","area":"CIRCLE (-23.5505 -46.6333, 300)","calendarId":${weekdays}}`).save("office", "id"),
		postJSON("admin", "/api/permissions", `{"deviceId":${hourlyDevice},"geofenceId":${office}}`),
		postJSON("admin", "/api/devices", `{"name":"Commuter","uniqueId":"100013"}`).save("commuter", "id"),
		postJSON("admin", "/api/permissions", `{"deviceId":${commuter},"geofenceId":${office}}`),

		// Devices of OsmAndProtocolDecoderTest's vectors and of the in-house clients.
		postJSON("admin", "/api/devices", `{"name":"V1","uniqueId":"48241179"}`),
		postJSON("admin", "/api/devices", `{"name":"V2","uniqueId":"658765"}`),
		postJSON("admin", "/api/devices", `{"name":"V3","uniqueId":"8737767034"}`),
		postJSON("admin", "/api/devices", `{"name":"V4","uniqueId":"123456"}`),
		postJSON("admin", "/api/devices", `{"name":"V5","uniqueId":"902064"}`),
		postJSON("admin", "/api/devices", `{"name":"V6","uniqueId":"353861053849681"}`),
		postJSON("admin", "/api/devices", `{"name":"V7","uniqueId":"861001000719969"}`),
		postJSON("admin", "/api/devices", `{"name":"V8","uniqueId":"123456789012345"}`),

		postJSON("admin", "/api/maintenance", `{"name":"Oil","type":"totalDistance","start":100,"period":1000}`).save("oil", "id"),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"maintenanceId":${oil}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${buddy},"linkedDeviceId":${car}}`),
		// Queued (the car isn't connected): OsmAnd returns it in a response body.
		postJSON("admin", "/api/commands/send", `{"deviceId":${car},"type":"custom","attributes":{"data":"hello car"}}`),
		postJSON("admin", "/api/commands/send", `{"deviceId":${car},"type":"custom","attributes":{"data":"second"}}`),
	}
	// Production's buffering (none). With buffering, Java releases each connection's fixes
	// on that connection's event loop thread, so a device's fixes arriving over several
	// connections are processed in a racy order; bufferedSuite covers buffering on one
	// connection, where Java's order is deterministic.
	config := map[string]string{"server.buffering.threshold": "0"}
	return suite{seed: seed, strictSeed: true, config: config, scenarios: []Scenario{
		{Name: "osmand-formats", Steps: []Step{
			osmand(
				// Query parameters: every named one, custom ones as numbers, booleans and
				// strings, an empty value, and a repeated name.
				"/?id=100007&lat=-23.5&lon=-46.6&timestamp=1790856000&speed=10&bearing=90&altitude=700&accuracy=5"+
					"&hdop=0.9&batt=55&charge=true&driverUniqueId=DRV1&custom=7&flag=true&off=false&word=abc&empty=&dup=1&dup=2.5",
				// deviceid, location, milliseconds, heading, valid=1, cells and Wi-Fi (Java
				// keeps them in HashSets).
				"/?deviceid=100007&location=-23.51,-46.61&timestamp=1790856060000&heading=180&valid=1"+
					"&cell=724,5,1234,56789,-70&cell=724,5,1234,56790&cell=724,10,99,1,-50&cell=724,5,1234,56789,-70"+
					"&wifi=aa-bb-cc-dd-ee-ff,-60&wifi=11:22:33:44:55:66,-40&wifi=00-00-00-00-00-01,-90",
				// ISO times: fraction, offset with a region, lower case; escapes, '+' and ';'.
				"/?id=100007&lat=-23.52&lon=-46.62&timestamp=2026-09-21T10:02:00.5Z&valid=false&note=S%C3%A3o+Paulo%20x;semi=1&bad=%C3%28",
				"/?id=100007&lat=-23.53&lon=-46.63&timestamp=2026-09-21T07:03:00-03:00%5BAmerica/Sao_Paulo%5D",
				"/?id=100007&lat=-23.535&lon=-46.635&timestamp=2026-09-21t10:03:30z",
				"/?id=100007&lat=-23.536&lon=-46.636&timestamp=2026-09-21T10:03:40%2B01",
				// Local time in the server's zone, a clamped day, and 24:00.
				"/?id=100007&lat=-23.54&lon=-46.64&timestamp=2026-09-21%2007:04:00",
				"/?id=100007&lat=-23.541&lon=-46.641&timestamp=2026-02-30%2007:04:00",
				"/?id=100007&lat=-23.542&lon=-46.642&timestamp=2026-02-27%2024:00:00",
				// Form body, and a query string that wins over the body.
				"POST /\nid=100007&lat=-23.56&lon=-46.66&timestamp=2026-09-21T10:06:00Z&speed=3.5",
				"POST /?id=100007&lat=-23.57&lon=-46.67&timestamp=2026-09-21T10:07:00Z\nignored=1",
				// Java number syntax: exponent, hex, suffixes, a signed timestamp.
				"/?id=100007&lat=1e1&lon=0x1p3&timestamp=2026-09-21T10:15:00Z",
				"/?id=100007&lat=-23.62d&lon=-46.72f&timestamp=2026-09-21T10:16Z",
				"/?id=100007&lat=-23.63&lon=-46.73&timestamp=%2B1790857000",
				// The fragment ends the parameters.
				"/?id=100007&lat=-23.61&lon=-46.71&timestamp=2026-09-21T10:14:00Z#frag=1",
				// Notification tokens only count after the id.
				"/?notificationToken=early&id=100007&lat=-23.59&lon=-46.69&timestamp=2026-09-21T10:11:00Z",
				"/?id=100007&lat=-23.591&lon=-46.691&timestamp=2026-09-21T10:11:30Z&notificationToken=tok123",
			),
			// No coordinates: the last known fix is reused (outdated).
			osmand("/?id=100007&timestamp=2026-09-21T10:20:00Z&batt=50"),
			// JSON (Transistorsoft background geolocation).
			osmand(`JSON /` + "\n" + `{"device_id":"100007","location":{"timestamp":"2026-09-21T10:21:00.000Z",
				"coords":{"latitude":-23.6,"longitude":-46.7,"speed":5.5,"heading":45,"accuracy":12,"altitude":701.5},
				"event":"motionchange","is_moving":true,"odometer":1234.9,"mock":false,"activity":{"type":"in_vehicle"},
				"battery":{"level":0.87,"is_charging":true},"alarm":"sos",
				"extras":{"alarm":"ignored","n":5,"d":5.0,"e":1e2,"b":true,"s":"x","empty":"","o":{"a":1},"z":null}}}`),
			osmand(`JSON /` + "\n" + `{"device_id":"100007","location":{"timestamp":"2026-09-21T10:22:00Z",
				"coords":{"latitude":-23.61,"longitude":-46.71,"speed":-1,"heading":-1,"accuracy":3,"altitude":0},
				"battery":{"level":-1,"is_charging":false}}}`),
			osmand(`JSON /` + "\n" + `{"device_id":"100007","location":{"timestamp":"2026-09-21T10:23:00Z"}}`),
			get("admin", "/api/positions?deviceId=${formats}&from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z"),
			get("admin", "/api/devices/${formats}"),
		}},

		// Every vector of OsmAndProtocolDecoderTest, in order.
		{Name: "osmand-vectors", Steps: []Step{
			osmand(
				`JSON /`+"\n"+`{"location":{"timestamp":"2025-06-15T13:45:12.862Z","coords":{"latitude":37.4219983,"longitude":-122.084,"accuracy":5,"speed":0,"heading":-1,"altitude":5},"is_moving":false,"odometer":0,"event":"motionchange","battery":{"level":1,"is_charging":false},"activity":{"type":"still"},"extras":{},"_":"&id=48241179&lat=37.4219983&lon=-122.084&timestamp=2025-06-15T13:45:12.862Z&"},"device_id":"48241179"}`,
				`JSON /`+"\n"+`{"location":{"timestamp":"2025-06-15T13:45:12.862Z","coords":{"latitude":37.4219983,"longitude":-122.084,"accuracy":5,"speed":0,"heading":-1,"altitude":5},"is_moving":false,"odometer":0,"event":"motionchange","battery":{"level":1,"is_charging":false},"activity":{"type":"still"},"extras":{"device_string_test":"long_device_id","device_bool_test":true,"device_bool_false_test":false,"device_integer_test":1,"device_integer_twelve_test":12,"device_double_test":1.345,"device_very_long_double_test":1763636.3442},"_":"&id=48241179&lat=37.4219983&lon=-122.084&timestamp=2025-06-15T13:45:12.862Z&"},"device_id":"48241179"}`,
				`JSON /`+"\n"+`{"location":{"extras":{},"mock":true,"coords":{"speed_accuracy":-1,"speed":-1,"longitude":-122.406417,"ellipsoidal_altitude":0,"floor":null,"heading_accuracy":-1,"latitude":37.785834000000001,"accuracy":5,"altitude_accuracy":-1,"altitude":0,"heading":-1},"is_moving":false,"age":188,"odometer":0,"uuid":"2FB04C65-99CF-42AB-8DD3-EBCB4B108BF8","event":"motionchange","battery":{"level":-1,"is_charging":false},"activity":{"type":"unknown","confidence":100},"timestamp":"2025-05-09T04:11:30.579Z"},"device_id":"658765"}`,
				`JSON /`+"\n"+`{"location":{"event":"motionchange","is_moving":false,"uuid":"0e9a2473-a9a7-4c00-997b-fb97d2154e75","timestamp":"2021-07-21T08:06:34.444Z","odometer":0,"coords":{"latitude":-6.1148096,"longitude":106.6837015,"accuracy":3.8,"speed":18.67,"speed_accuracy":0.26,"heading":63,"heading_accuracy":0.28,"altitude":35.7,"altitude_accuracy":3.8},"activity":{"type":"still","confidence":100},"battery":{"is_charging":false,"level":0.79},"extras":{}},"device_id":"8737767034"}`,
				"/?id=123456&timestamp=1377177267&cell=257,02,16,2224&cell=257,02,16,2223,-90&wifi=00-14-22-01-23-45,-80&wifi=00-1C-B3-09-85-15,-70",
				"/?timestamp=1377177267&lat=60.0&lon=30.0",
				"/?id=902064&lat=42.06288&lon=-88.23412&timestamp=2016-01-27T18%3A55%3A47Z&hdop=6.0&altitude=224.0&speed=0.0",
				"/?id=902064&lat=42.06288&lon=-88.23412&timestamp=1442068686579&hdop=6.0&altitude=224.0&speed=0.0",
				"/?lat=49.60688&lon=6.15788&timestamp=2014-06-04+09%3A10%3A11&altitude=384.7&speed=0.0&id=353861053849681",
				"/?id=123456&timestamp=1377177267&lat=60.0&lon=30.0&speed=0.0&bearing=0.0&altitude=0&hdop=0.0",
				"/?id=123456&timestamp=1377177267&lat=60.0&lon=30.0",
				"/?lat=60.0&lon=30.0&speed=0.0&heading=0.0&vacc=0&hacc=0&altitude=0&deviceid=123456",
				"/?id=861001000719969&lat=41.666667&lon=-0.883333&altitude=350.059479&speed=0.000000&batt=87",
				"/?id=123456&timestamp=1377177267&location=60.0,30.0",
				"/?id=123456789012345&timestamp=1504763810&lat=40.7232948571&lon=-74.0061408571&bearing=7.19889788244&speed=40&ignition=true&rpm=933&fuel=24",
			),
			// The in-house clients (inventory/clients.md): traccar-bots posts the query only;
			// track-client-lib posts a form body, with UTC or local-offset timestamps.
			osmand(
				"POST /?id=123456&lat=-23.5299&lon=-46.6099&timestamp=2026-10-01T13:00:00.123Z&speed=1.94&bearing=90&altitude=760&accuracy=8&valid=true\n",
				"POST /\nid=123456&lat=-23.5298&lon=-46.6098&timestamp=2026-10-01T13:01:00.000Z&speed=3.8876&heading=180.5&accuracy=12.3&altitude=761.2&batt=87.0&charge=true&is_moving=true&odometer=0&activity=in_vehicle&os=android",
				"POST /\nid=123456&lat=-23.5297&lon=-46.6097&timestamp=2026-10-01T10:02:00.000-03:00&speed=0&heading=0&accuracy=5&altitude=760&batt=86.0&charge=false&is_moving=false&odometer=0&activity=still&os=ios",
			),
		}},

		{Name: "osmand-errors", Steps: []Step{
			osmand(
				"/?id=999999&lat=1&lon=2",
				"/?lat=1&lon=2",
				`JSON /`+"\n"+`{"device_id":"999999","location":{"timestamp":"2026-09-21T10:00:00Z"}}`,
				"/?id=100007&lat=abc&lon=1",
				"/?id=100007&lat=91&lon=1",
				"/?id=100007&lat=1&lon=181",
				"/?id=100005&lat=1&lon=1",
				"/?id=100007&lat=1&lon=1&timestamp=2026-02-30T00:00:00Z",
				"/?id=100007&lat=1&lon=1&timestamp=1e9",
				"/?id=100007&lat=1&lon=1&timestamp=2026-09-21T10:00:00",
				"/?id=100007&lat=1&lon=2&cell=1,2,3",
				"/?id=100007&location=5",
				"/?id=100007&lat=1&lon=1&wifi=aa",
				`JSON /`+"\n"+`{"device_id":100007}`,
				`JSON /`+"\n"+`{"device_id":"100007","location":{"timestamp":"2026-09-21T10:00:00Z","coords":{"latitude":1}}}`,
				`JSON /`+"\n"+`not json`,
				"RAW GET /?id=100007&lat=1&lon=1&x=%zz HTTP/1.1\r\nHost: x\r\n\r\n",
				"RAW GET /?id=100007&lat=-23.5&lon=-46.6&timestamp=2026-09-21T11:00:00Z&n=\xc3\xa3o HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n",
			),
			get("admin", "/api/devices"),
		}},

		{Name: "osmand-queue", Steps: []Step{
			// The first message takes the oldest queued command, the next the other one.
			osmand(
				"/?id=100001&lat=-23.5505&lon=-46.6333&timestamp=2026-10-10T07:59:00Z",
				"/?id=100001&lat=-23.5505&lon=-46.6333&timestamp=2026-10-10T07:59:30Z",
				"/?id=100001&lat=-23.5505&lon=-46.6333&timestamp=2026-10-10T07:59:40Z",
			),
		}},

		{Name: "osmand-drive", Steps: drive()},

		{Name: "osmand-filters", Steps: []Step{
			osmand(
				"/?id=100002&lat=-23.5&lon=-46.6&timestamp=2026-10-10T09:00:00Z&speed=5&accuracy=10",
				"/?id=100002&lat=0&lon=0&timestamp=2026-10-10T09:01:00Z&speed=5",
			),
			osmand(
				// Duplicate (same time, no new attributes), static (speed 0), too soon,
				// inaccurate, too fast; an alarm change skips the excess-data filters.
				"/?id=100002&lat=-23.5&lon=-46.6&timestamp=2026-10-10T09:00:00Z&speed=5&accuracy=10",
				"/?id=100002&lat=-23.501&lon=-46.6&timestamp=2026-10-10T09:05:00Z&speed=0",
				"/?id=100002&lat=-23.502&lon=-46.6&timestamp=2026-10-10T09:05:10Z&speed=5",
				"/?id=100002&lat=-23.503&lon=-46.6&timestamp=2026-10-10T09:10:00Z&speed=5&accuracy=80",
				"/?id=100002&lat=-22.0&lon=-46.6&timestamp=2026-10-10T09:15:00Z&speed=5",
				"/?id=100002&lat=-23.504&lon=-46.6&timestamp=2026-10-10T09:20:00Z&speed=0&alarm=sos",
				"/?id=100002&lat=-23.505&lon=-46.6&timestamp=2030-10-10T09:20:00Z&speed=5",
			),
		}},

		{Name: "osmand-zones", Steps: []Step{
			// Inside Home, but the device also has a geofence Java can't parse: no zones.
			osmand("/?id=100003&lat=-23.5505&lon=-46.6333&timestamp=2026-10-10T10:00:00Z&speed=1"),
			osmand("/?id=100003&lat=-23.0&lon=-46.0&timestamp=2026-10-10T10:05:00Z&speed=1"),
			// A decimal-comma circle fails the same way for this device.
			osmand("/?id=100009&lat=-23.5505&lon=-46.6333&timestamp=2026-10-10T10:00:00Z&speed=1"),
		}},

		{Name: "osmand-proximity", Steps: []Step{
			// Both devices on one connection, so the car stays cached for the buddy's checks.
			osmand(
				"/?id=100001&lat=-23.60&lon=-46.70&timestamp=2026-10-11T08:00:00Z&speed=0",
				"/?id=100004&lat=-23.62&lon=-46.70&timestamp=2026-10-11T08:00:00Z&speed=0&motion=false",
			),
			osmand(
				"/?id=100001&lat=-23.60&lon=-46.70&timestamp=2026-10-11T08:01:00Z&speed=0",
				"/?id=100004&lat=-23.601&lon=-46.70&timestamp=2026-10-11T08:01:00Z&speed=0",
				"/?id=100004&lat=-23.61&lon=-46.70&timestamp=2026-10-11T08:02:00Z&speed=10&motion=true",
				"/?id=100004&lat=-23.70&lon=-46.70&timestamp=2026-10-11T08:03:00Z&speed=10&motion=false",
				"/?id=100004&lat=-23.71&lon=-46.70&timestamp=2026-10-11T08:04:00Z&speed=10&motion=true",
			),
		}},

		{Name: "osmand-calendars", Steps: []Step{
			// Weekdays 08:00 to 18:00 local: a recurring occurrence includes both its ends.
			osmand(
				"/?id=100010&lat=-23.5&lon=-46.6&timestamp=2026-10-09%2010:00:00",
				"/?id=100010&lat=-23.5&lon=-46.6&timestamp=2026-10-10%2010:00:00",
				"/?id=100010&lat=-23.5&lon=-46.6&timestamp=2026-10-12%2007:59:59",
				"/?id=100010&lat=-23.5&lon=-46.6&timestamp=2026-10-12%2008:00:00",
				"/?id=100010&lat=-23.5&lon=-46.6&timestamp=2026-10-12%2018:00:00",
				"/?id=100010&lat=-23.5&lon=-46.6&timestamp=2026-10-12%2018:00:01",
			),
			// Every two hours from 08:00 local, in December and January.
			osmand(
				"/?id=100011&lat=-23.5505&lon=-46.6333&timestamp=2025-12-10%2010:30:00",
				"/?id=100011&lat=-23.5505&lon=-46.6333&timestamp=2025-12-10%2011:30:00",
				"/?id=100011&lat=-23.5505&lon=-46.6333&timestamp=2026-01-10%2010:30:00",
				"/?id=100011&lat=-23.5505&lon=-46.6333&timestamp=2026-01-10%2011:30:00",
				"/?id=100011&lat=-23.0&lon=-46.0&timestamp=2026-01-10%2012:30:00",
				"/?id=100011&lat=-23.5505&lon=-46.6333&timestamp=2026-03-10%2012:30:00",
			),
			// Office geofence events count only within its weekday schedule: in on Friday,
			// out and in again over the weekend (no events), out on Monday evening, in on
			// Tuesday at 08:00 sharp.
			osmand(
				"/?id=100013&lat=-23.0&lon=-46.0&timestamp=2026-10-09%2009:00:00",
				"/?id=100013&lat=-23.5505&lon=-46.6333&timestamp=2026-10-09%2010:00:00",
				"/?id=100013&lat=-23.0&lon=-46.0&timestamp=2026-10-10%2010:00:00",
				"/?id=100013&lat=-23.5505&lon=-46.6333&timestamp=2026-10-11%2010:00:00",
				"/?id=100013&lat=-23.0&lon=-46.0&timestamp=2026-10-12%2018:00:00",
				"/?id=100013&lat=-23.5505&lon=-46.6333&timestamp=2026-10-13%2008:00:00",
			),
			// A single event excludes its start and includes neither end.
			osmand(
				"/?id=100012&lat=-23.5&lon=-46.6&timestamp=2026-10-13%2008:00:00",
				"/?id=100012&lat=-23.5&lon=-46.6&timestamp=2026-10-13%2008:00:01",
				"/?id=100012&lat=-23.5&lon=-46.6&timestamp=2026-10-13%2009:00:00",
			),
		}},

		{Name: "osmand-hours", Steps: []Step{
			osmand(
				"/?id=100009&lat=-23.0&lon=-46.0&timestamp=2026-10-12T08:00:00Z&ignition=true&door=open&fuel=50",
				"/?id=100009&lat=-23.0&lon=-46.0&timestamp=2026-10-12T08:10:00Z&ignition=true&speed=3",
				"/?id=100009&lat=-23.0&lon=-46.0&timestamp=2026-10-12T08:20:00Z&ignition=false&speed=2",
				"/?id=100009&lat=-23.0&lon=-46.0&timestamp=2026-10-12T08:30:00Z&ignition=true&hours=5000",
				"/?id=100009&lat=-23.0&lon=-46.0&timestamp=2026-10-12T08:40:00Z&ignition=true&motion=true",
			),
			// Server time replaces the device's (time.override).
			osmand("/?id=100006&lat=-23.0&lon=-46.0&timestamp=2026-01-01T00:00:00Z"),
			// A device without positions sending no coordinates: the GPS epoch.
			osmand("/?id=100008&timestamp=2026-10-12T08:00:00Z"),
		}},
	}}
}

// drive is a car trip through the geofences with the events of every detector: geofence
// enter and exit, motion, overspeed (group and geofence limits), alarms, ignition, fuel,
// driver changes, maintenance, command results, media, and an out-of-order fix.
func drive() []Step {
	start := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	at := func(minutes float64) string {
		return start.Add(time.Duration(minutes * float64(time.Minute))).Format("2006-01-02T15:04:05Z")
	}
	type fix struct {
		min      float64
		lat, lon float64
		speed    float64
		extra    string
	}
	fixes := [][]fix{
		{ // parked at home
			{0, -23.5505, -46.6333, 0, "&ignition=false&fuel=60&driverUniqueId=D1"},
			{1, -23.5505, -46.6333, 0, "&ignition=true&fuel=60&driverUniqueId=D1&image=a.jpg"},
		},
		{ // leaving home towards the zone, speeding
			{3, -23.5480, -46.6300, 20, "&fuel=59"},
			{5, -23.5400, -46.6200, 35, "&fuel=58&alarm=sos,overspeed&totalDistance=99"},
			{6, -23.5395, -46.6195, 36, "&alarm=sos,lowBattery,sos"},
			{7, -23.5350, -46.6150, 20, "&fuel=45&result=OK&driverUniqueId=D2"},
		},
		{ // on the road (polyline, its own speed limit), with a fix arriving late
			{9, -23.5300, -46.6100, 41, "&odometer=1000"},
			{12, -23.5200, -46.6000, 45, "&distance=1500.5&video=v.mp4"},
			{11, -23.5250, -46.6050, 30, ""},
			{14, -23.5100, -46.5900, 10, "&fuel=70&alarm=powerCut"},
		},
		{ // stopped long enough for a stop
			{18, -23.5100, -46.5900, 0, "&ignition=false"},
			{22, -23.5100, -46.5901, 0, ""},
			{30, -23.5100, -46.5900, 0, "&audio=r.wav"},
		},
		{ // a jump after a long gap (stop gap logic)
			{120, -23.3000, -46.3000, 0, "&ignition=true"},
			{121, -23.3010, -46.3010, 15, ""},
		},
	}
	var steps []Step
	for _, batch := range fixes {
		var msgs []string
		for _, f := range batch {
			msgs = append(msgs, fmt.Sprintf("/?id=100001&lat=%v&lon=%v&timestamp=%s&speed=%v%s",
				f.lat, f.lon, at(f.min), f.speed, f.extra))
		}
		steps = append(steps, osmand(msgs...))
	}
	steps = append(steps,
		get("admin", "/api/positions?deviceId=${car}&from=2026-10-10T00:00:00Z&to=2026-10-11T00:00:00Z"),
		get("admin", "/api/devices/${car}"),
	)
	return steps
}

// bufferedSuite is server.buffering.threshold (5 s, as in sir-dev.xml) on one connection:
// fixes are released in fix time order, later fixes waiting again whenever an earlier one
// arrives, and an exact duplicate is released too.
func bufferedSuite() suite {
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/devices", `{"name":"Car","uniqueId":"100001"}`).save("car", "id"),
	}
	return suite{seed: seed, strictSeed: true, scenarios: []Scenario{
		{Name: "osmand-buffered", Steps: []Step{
			osmand(
				"/?id=100001&lat=-23.50&lon=-46.60&timestamp=2026-10-10T08:05:00Z&speed=10",
				"/?id=100001&lat=-23.51&lon=-46.61&timestamp=2026-10-10T08:01:00Z&speed=10",
				"/?id=100001&lat=-23.52&lon=-46.62&timestamp=2026-10-10T08:03:00Z&speed=10",
				"/?id=100001&lat=-23.53&lon=-46.63&timestamp=2026-10-10T08:03:30Z&speed=10",
				"/?id=100001&lat=-23.54&lon=-46.64&timestamp=2026-10-10T08:00:00Z&speed=0",
				"/?id=100001&lat=-23.55&lon=-46.65&timestamp=2026-10-10T08:10:00Z&speed=0",
			),
			osmand(
				"/?id=100001&lat=-23.56&lon=-46.66&timestamp=2026-10-10T08:20:00Z&speed=10",
				"/?id=100001&lat=-23.57&lon=-46.67&timestamp=2026-10-10T08:15:00Z&speed=10",
			),
		}},
	}}
}

// statusSuite is device status with event.status.enable: online on each message,
// unknown after status.timeout without one (a status event each time it changes), and
// database.registerUnknown creating devices for unknown ids that match its pattern.
func statusSuite() suite {
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/devices", `{"name":"Car","uniqueId":"100001"}`).save("car", "id"),
	}
	config := map[string]string{
		"server.buffering.threshold":               "0",
		"event.status.enable":                      "true",
		"status.timeout":                           "4",
		"database.registerUnknown":                 "true",
		"database.registerUnknown.defaultCategory": "car",
	}
	return suite{seed: seed, strictSeed: true, config: config, scenarios: []Scenario{
		{Name: "status", Steps: []Step{
			osmand(
				"/?id=100001&lat=-23.5&lon=-46.6&timestamp=2026-10-10T08:00:00Z",
				"/?id=100001&lat=-23.5&lon=-46.6&timestamp=2026-10-10T08:01:00Z",
				"/?id=new777&lat=-23.5&lon=-46.6&timestamp=2026-10-10T08:02:00Z",
				"/?id=x&lat=-23.5&lon=-46.6&timestamp=2026-10-10T08:02:00Z",
			),
			get("admin", "/api/devices"),
			{Sleep: 7 * time.Second},
			get("admin", "/api/devices"),
			osmand("/?id=100001&lat=-23.5&lon=-46.6&timestamp=2026-10-10T08:10:00Z"),
			get("admin", "/api/devices"),
		}},
	}}
}
