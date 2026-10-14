package main

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
)

// library returns the scenarios in the order they run. Later scenarios use users,
// devices and variables created by earlier ones.
//
// Device payloads are taken from the Java protocol tests (src/test/java/org/traccar/
// protocol), so every message is a real device format.
func library() []Scenario {
	return []Scenario{
		{Name: "bootstrap", Steps: []Step{
			serverInfo(""),
			get("", "/api/session"),
			get("", "/api/health"),
			// The first user on an empty database becomes administrator.
			postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret",
				"attributes":{"double":87.0,"int":87,"long":12345678901234,"big":1.5e300,"small":1.0E-5,
				"odometer":12345678.9,"text":"São Paulo </x>  ","bool":true,"nested":{"z":1,"a":[2.0,"x"]}}}`).
				save("admin", "id"),
			// Registration is closed now that a user exists.
			postJSON("", "/api/users", `{"name":"Intruder","email":"intruder@example.com","password":"x"}`),
			postForm("admin", "/api/session", "email=admin%40example.com&password=wrong"),
			login("admin", "admin@example.com", "secret"),
			get("admin", "/api/session"),
			serverInfo("admin"),
			get("admin", "/api/server/timezones"),
			get("admin", "/api/notifications/types"),
			get("admin", "/api/notifications/notificators").unordered("$"),
		}},

		{Name: "server-settings", Steps: []Step{
			putJSON("admin", "/api/server", `{"id":1,"registration":true,"readonly":false,"deviceReadonly":false,
				"map":"osm","latitude":-23.5505,"longitude":-46.6333,"zoom":10,"forceSettings":false,
				"coordinateFormat":"dd","limitCommands":false,"disableReports":false,"fixedEmail":false,
				"announcement":"Manutenção às 22h","attributes":{"timezone":"America/Sao_Paulo","speedUnit":"kmh"}}`),
			serverInfo(""),
			// Open registration: anonymous sign-up creates a regular user.
			postJSON("", "/api/users", `{"name":"Self","email":"self@example.com","password":"self"}`).save("self", "id"),
			login("self", "self@example.com", "self"),
			get("self", "/api/session"),
			putJSON("admin", "/api/server", `{"id":1,"registration":false,"readonly":false,"map":"osm",
				"latitude":-23.5505,"longitude":-46.6333,"zoom":10,"coordinateFormat":"dd",
				"attributes":{"timezone":"America/Sao_Paulo","speedUnit":"kmh"}}`),
		}},

		{Name: "users-and-roles", Steps: []Step{
			postJSON("admin", "/api/users", `{"name":"Manager","email":"manager@example.com","password":"m",
				"userLimit":5,"deviceLimit":20,"attributes":{"web.liveRouteLength":10}}`).save("manager", "id"),
			postJSON("admin", "/api/users", `{"name":"User","email":" user@example.com ","password":"u",
				"phone":" +55 11 99999-0000 ","deviceLimit":10,"attributes":{"speedUnit":"kmh"}}`).save("user", "id"),
			postJSON("admin", "/api/users", `{"name":"Readonly","email":"readonly@example.com","password":"r","readonly":true}`).
				save("readonly", "id"),
			postJSON("admin", "/api/users", `{"name":"Disabled","email":"disabled@example.com","password":"d","disabled":true}`),
			postJSON("admin", "/api/users", `{"name":"Expired","email":"expired@example.com","password":"e",
				"expirationTime":"2020-01-01T00:00:00.000+00:00"}`),
			login("manager", "manager@example.com", "m"),
			login("user", "user@example.com", "u"),
			login("readonly", "readonly@example.com", "r"),
			postForm("disabled", "/api/session", "email=disabled%40example.com&password=d"),
			postForm("expired", "/api/session", "email=expired%40example.com&password=e"),
			// A manager creates a managed user (tc_user_user link).
			postJSON("manager", "/api/users", `{"name":"Managed","email":"managed@example.com","password":"x"}`).
				save("managed", "id"),
			get("admin", "/api/users"),
			get("manager", "/api/users"),
			get("user", "/api/users"),
			get("admin", "/api/users/${user}"),
			get("user", "/api/users/${manager}"),
			putJSON("admin", "/api/users/${user}", `{"id":${user},"name":"User Renamed","email":"user@example.com",
				"phone":"+5511999990000","deviceLimit":10,"map":"osm","zoom":3,"latitude":1.0,"longitude":-2.5,
				"attributes":{"speedUnit":"kmh","timezone":"America/Sao_Paulo"}}`),
			putJSON("user", "/api/users/${manager}", `{"id":${manager},"name":"Hijack","email":"manager@example.com"}`),
			postJSON("readonly", "/api/users", `{"name":"Nope","email":"nope@example.com","password":"x"}`),
			postWith("admin", "/api/users/totp").opaqueBody(),
		}},

		{Name: "tokens-and-auth", Steps: []Step{
			postForm("admin", "/api/session/token", "expiration=2035-01-01T00:00:00Z").save("token", "$body").opaqueBody().token(),
			get("", "/api/devices").header("Authorization", "Bearer ${token}"),
			get("tokenuser", "/api/session?token=${token}"),
			get("tokenuser", "/api/session"),
			get("", "/api/devices").header("Authorization", "Basic "+basic("admin@example.com", "secret")),
			get("", "/api/devices").header("Authorization", "Basic "+basic("admin@example.com", "wrong")),
			get("", "/api/devices").header("Authorization", "Bearer not-a-token"),
			postForm("admin", "/api/session/token/revoke", "token=${token}"),
			get("", "/api/devices").header("Authorization", "Bearer ${token}"),
			del("tokenuser", "/api/session"),
			get("tokenuser", "/api/session"),
		}},

		{Name: "entities-crud", Steps: []Step{
			postJSON("admin", "/api/groups", `{"name":"Fleet","attributes":{"speedLimit":27.0}}`).save("fleet", "id"),
			postJSON("admin", "/api/groups", `{"name":"Fleet / North","groupId":${fleet},"attributes":{}}`).save("north", "id"),
			postJSON("admin", "/api/devices", `{"name":"Phone","uniqueId":"123456","groupId":${north},
				"phone":"+5511999999999","model":"Android","contact":"Ana","category":"car",
				"attributes":{"speedLimit":80,"fuel.capacity":55.5}}`).save("osmand", "id"),
			postJSON("admin", "/api/devices", `{"name":"Suntech ST4","uniqueId":"807587388","groupId":${fleet}}`).save("st4", "id"),
			postJSON("admin", "/api/devices", `{"name":"Suntech STT","uniqueId":"0560001616"}`).save("stt", "id"),
			postJSON("admin", "/api/devices", `{"name":"Suntech ST300","uniqueId":"109002029"}`).save("st300", "id"),
			postJSON("admin", "/api/devices", `{"name":"Suntech binary SIR","uniqueId":"1051134087"}`).save("stbin", "id"),
			postJSON("admin", "/api/devices", `{"name":"Intellitrac 1","uniqueId":"359316075744331"}`).save("itr1", "id"),
			postJSON("admin", "/api/devices", `{"name":"Intellitrac 2","uniqueId":"101000001"}`).save("itr2", "id"),
			postJSON("admin", "/api/devices", `{"name":"Atrack 1","uniqueId":"356961075931165"}`).save("at1", "id"),
			postJSON("admin", "/api/devices", `{"name":"Atrack 2","uniqueId":"357298070426498"}`).save("at2", "id"),
			postJSON("admin", "/api/devices", `{"name":"Atrack 3","uniqueId":"358683064932578"}`).save("at3", "id"),
			postJSON("admin", "/api/devices", `{"name":"Duplicate","uniqueId":"123456"}`),
			postJSON("admin", "/api/devices", `{"name":"Expires","uniqueId":"EXP-1","expirationTime":"2030-10-14T19:11:35.1+0300"}`).
				save("expiring", "id"),

			postJSON("admin", "/api/geofences", `{"name":"Home","description":"Circle","area":"CIRCLE (-23.5505 -46.6333, 500)",
				"attributes":{"speedLimit":20.0}}`).save("home", "id"),
			postJSON("admin", "/api/geofences", `{"name":"Zone","area":"POLYGON ((-23.5 -46.6, -23.6 -46.6, -23.6 -46.7, -23.5 -46.7, -23.5 -46.6))"}`).
				save("zone", "id"),
			// Longer than VARCHAR(4096): fails unless changelog-6.13-dsc (area TEXT) is applied.
			postJSON("admin", "/api/geofences", `{"name":"Big","area":"`+bigPolygon(400)+`"}`),
			postJSON("admin", "/api/drivers", `{"name":"Driver One","uniqueId":"22574684","attributes":{}}`).save("driver", "id"),
			postJSON("admin", "/api/attributes/computed", `{"description":"Speed km/h","attribute":"speedKmh",
				"expression":"speed * 1.852","type":"number","priority":0}`).save("computed", "id"),
			postJSON("admin", "/api/calendars", `{"name":"Business hours","data":"`+calendarData+`","attributes":{}}`).
				save("calendar", "id"),
			postJSON("admin", "/api/commands", `{"description":"Reboot","type":"rebootDevice","textChannel":false,"attributes":{}}`).
				save("command", "id"),
			postJSON("admin", "/api/notifications", `{"type":"geofenceEnter","notificators":"web","always":false,
				"calendarId":${calendar},"attributes":{}}`).save("notification", "id"),
			postJSON("admin", "/api/notifications", `{"type":"alarm","notificators":"web","always":true,
				"attributes":{"alarms":"sos,overspeed"}}`).save("alarmNotification", "id"),
			postJSON("admin", "/api/maintenance", `{"name":"Oil","type":"totalDistance","start":10000.0,"period":5000,"attributes":{}}`).
				save("maintenance", "id"),
			postJSON("admin", "/api/orders", `{"uniqueId":"O-1","description":"Delivery","fromAddress":"A","toAddress":"B","attributes":{}}`).
				save("order", "id"),
			postJSON("admin", "/api/reports", `{"type":"route","description":"Daily route","calendarId":${calendar},"attributes":{}}`).
				save("report", "id"),

			get("admin", "/api/groups"),
			get("admin", "/api/devices"),
			get("admin", "/api/devices?uniqueId=123456"),
			get("admin", "/api/devices/${osmand}"),
			get("admin", "/api/geofences"),
			get("admin", "/api/drivers"),
			get("admin", "/api/attributes/computed"),
			get("admin", "/api/calendars"),
			get("admin", "/api/commands"),
			get("admin", "/api/notifications"),
			get("admin", "/api/maintenance"),
			get("admin", "/api/orders"),
			get("admin", "/api/reports"),

			putJSON("admin", "/api/devices/${st4}", `{"id":${st4},"name":"Suntech ST4 (bus 12)","uniqueId":"807587388",
				"groupId":${fleet},"calendarId":${calendar},"attributes":{"speedLimit":43.2}}`),
			putJSON("admin", "/api/geofences/${zone}", `{"id":${zone},"name":"Zone 2",
				"area":"POLYGON ((-23.5 -46.6, -23.6 -46.6, -23.6 -46.7, -23.5 -46.6))","calendarId":${calendar}}`),
			putJSON("admin", "/api/maintenance/${maintenance}", `{"id":${maintenance},"name":"Oil change","type":"totalDistance",
				"start":10000,"period":5000.5,"attributes":{"note":"x"}}`),
			putJSON("admin", "/api/attributes/computed/${computed}", `{"id":${computed},"description":"Speed km/h",
				"attribute":"speedKmh","expression":"speed * 1.852","type":"number","priority":5}`),
			del("admin", "/api/orders/${order}"),
			get("admin", "/api/orders"),
		}},

		{Name: "permissions", Steps: []Step{
			postJSON("admin", "/api/permissions", `{"userId":${user},"deviceId":${osmand}}`),
			postJSON("admin", "/api/permissions", `{"userId":${manager},"groupId":${fleet}}`),
			postJSON("admin", "/api/permissions", `{"userId":${readonly},"deviceId":${st4}}`),
			// Bulk requires every entry to have the same key pair; a mixed list is a 400.
			postJSON("admin", "/api/permissions/bulk", `[{"deviceId":${osmand},"geofenceId":${home}},
				{"groupId":${fleet},"geofenceId":${zone}}]`),
			postJSON("admin", "/api/permissions/bulk", `[{"deviceId":${osmand},"geofenceId":${home}},
				{"deviceId":${st4},"geofenceId":${home}}]`),
			postJSON("admin", "/api/permissions", `{"groupId":${fleet},"geofenceId":${zone}}`),
			postJSON("admin", "/api/permissions", `{"deviceId":${st4},"driverId":${driver}}`),
			postJSON("admin", "/api/permissions", `{"userId":${user},"notificationId":${notification}}`),
			postJSON("admin", "/api/permissions", `{"deviceId":${osmand},"attributeId":${computed}}`),
			postJSON("admin", "/api/permissions", `{"deviceId":${osmand},"maintenanceId":${maintenance}}`),
			postJSON("admin", "/api/permissions", `{"userId":${user},"calendarId":${calendar}}`),
			postJSON("admin", "/api/permissions", `{"deviceId":${st4},"commandId":${command}}`),
			postJSON("admin", "/api/permissions", `{"deviceId":${osmand},"linkedDeviceId":${st4}}`),
			postJSON("admin", "/api/permissions", `{"userId":${manager},"managedUserId":${user}}`),
			postJSON("user", "/api/permissions", `{"userId":${user},"deviceId":${st300}}`),
			get("user", "/api/devices"),
			get("manager", "/api/devices"),
			get("manager", "/api/devices?all=true"),
			get("admin", "/api/devices?userId=${user}"),
			get("readonly", "/api/devices"),
			get("user", "/api/geofences"),
			get("user", "/api/geofences?deviceId=${osmand}"),
			get("admin", "/api/geofences?groupId=${fleet}"),
			get("user", "/api/notifications"),
			get("manager", "/api/users"),
			get("admin", "/api/permissions?userId=${user}&deviceId=0"),
			get("admin", "/api/permissions?deviceId=${osmand}&geofenceId=0"),
			postJSON("readonly", "/api/devices", `{"name":"Nope","uniqueId":"NOPE"}`),
			putJSON("readonly", "/api/devices/${st4}", `{"id":${st4},"name":"Renamed","uniqueId":"807587388"}`),
			delJSON("admin", "/api/permissions", `{"userId":${readonly},"deviceId":${st4}}`),
			delJSON("admin", "/api/permissions/bulk", `[{"deviceId":${st4},"geofenceId":${home}}]`),
			get("readonly", "/api/devices"),
		}},

		{Name: "device-traffic", Steps: []Step{
			// OsmAnd over HTTP: a short drive and a stop, for the trip and stop reports.
			osmand(
				"/?id=123456&lat=-23.5505&lon=-46.6333&timestamp=2026-10-01T12:00:00Z&speed=0&bearing=0&altitude=760&batt=87&hdop=1.5",
				"/?id=123456&lat=-23.5480&lon=-46.6300&timestamp=2026-10-01T12:05:00Z&speed=20&bearing=45&altitude=761.5&batt=86",
				"/?id=123456&lat=-23.5400&lon=-46.6200&timestamp=2026-10-01T12:10:00Z&speed=25.5&bearing=50&altitude=765&batt=85",
				"/?id=123456&lat=-23.5300&lon=-46.6100&timestamp=1790856900&speed=30&bearing=50&accuracy=4.5",
				"/?id=123456&lat=-23.5300&lon=-46.6100&timestamp=2026-10-01T12:30:00Z&speed=0&bearing=50",
				"/?id=123456&lat=-23.5300&lon=-46.6100&timestamp=2026-10-01T12:45:00Z&speed=0&bearing=50&alarm=sos",
				"/?id=999999&lat=1&lon=2&timestamp=2026-10-01T12:00:00Z",
			),
			// Filtered out: this device's calendar (set in entities-crud) starts in 2026.
			tcp("suntech",
				text("ST419STT;807587388;306;41;04;30062;724;2;-65;5221;255;30061;724;2;5221;0;0;30212;724;2;5221;0;0;30071;724;2;5221;0;0;30222;724;2;5221;0;0;30213;724;2;5221;0;0;30063;724;2;5221;0;0;3.92;1;0185;1;00010000;26.1;1;20240729;19:22:43;-22.754650;-043.437569;000.185;297.35;6;1\r"),
			),
			tcp("suntech",
				text("STT;0560001616;BFFFFF;56;1.0.15;1;20200219;20:52:25;00008D6C;334;20;0925;24;+20.741764;-103.430364;0.00;0.00;19;1;00000001;00000000;2;1;1765;00008003;0.0;12.14;136598\r"),
				text("ST300STT;109002029;08;1080;20190220;13:00:55;85405;+04.645710;-074.078525;007.760;005.19;10;1;6520802;13.86;100100;4;1716;0000039863;4.1;1;0.00;0000;0000;0;0\r"),
			),
			// SIR vectors (SuntechProtocolDecoderTest.testDecodeTwosComplement and
			// testDecodeBrazilianCoordinates). The current jar rejects the two's-complement
			// ones ("Latitude out of range") and then closes the connection, so each vector
			// gets its own. See MIGRATION.md, "Findings during Steps 1 and 2".
			tcp("suntech", hexBytes("81001210511340870008301903130d1e00ff676980")),
			tcp("suntech", hexBytes("81001610511340870018301903130d1e00fe98a6dcfd386faa")),
			tcp("suntech", hexBytes("81001610511340870018301903130d1e008167592482c79054")),
			tcp("intellitrac",
				text("359316075744331,20201008181424,12.014662,57.826301,0,76,24,10,997,3,0,0.000,4.208,20201008181424,0\r\n"),
				text("$OK:TRACKING\r\n"),
				text("101000001,20100304075545,121.64547,25.06200,0,0,61,7,2,1,0,0.046,0.000,20100304075546,0\r\n"),
			),
			tcp("atrack",
				atrackText("@P,CA4B,0,1,358683064932578,1533633976,1533633975,1533633975,121562641,25082649,72,0,1638,15,0,0,1,0,,2000,2000,,%CI%MV%BV,143,0\r\n"),
			),
			tcp("atrack",
				atrackText("@P,0EBB,0,1917,357298070426498,1534718280,1534718279,1534739774,-88643875,44210148,270,2,57128,6,1,130,0,0,,2000,2000,,%CI%GQ%GS%CN%CE%MV%SA,5,10,310260,7888593,137,16\r\n"+
					"1534718283,1534718283,1534739774,-88645243,44210145,269,2,57129,6,1,129,0,0,,2000,2000,,%CI%GQ%GS%CN%CE%MV%SA,5,10,310260,7888593,137,16\r\n"),
			),
			tcp("atrack",
				atrackText("@P,6254,0,989,356961075931165,1534381563,1534381564,1534381564,-88429188,44271225,70,2,200563,8,1,0,0,0,,2000,2000,,%CI%CE%CN%GQ%GS%FL%ML%VN%PD%FC%EL%ET%CD%AT%MF%MV,0,310260,18,9,0,0,2T1KR32E28C706185,0,0,0,54,8901260881215247759,252,489,123\r\n"),
			),
			// Binary custom Atrack frame (unit not registered): only the ACK is compared.
			tcp("atrack",
				hexBytes("405025e30096eb730001452efaf6a7d6562fe4f8562fe4f7562fe52c02a006d902273f810064650000e0f5000a0100000000000007d007d000254349255341254d5625425625475125415400090083002a1a000001a8562fe4f8562fe4f7562fe52c02a006d902273f810064020000e0f5000a0100000000000007d007d000254349255341254d5625425625475125415400090083002a1a000001a8"),
			),
			udp("atrack",
				atrackText("@P,CA4B,0,2,358683064932578,1533634000,1533634000,1533634000,121562641,25082649,72,0,1638,15,0,0,1,0,,2000,2000,,%CI%MV%BV,143,0\r\n"),
			),
			get("admin", "/api/devices"),
			get("admin", "/api/positions"),
			get("admin", "/api/positions?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z").
				save("lastPosition", "5.id"),
			get("admin", "/api/positions?id=${lastPosition}"),
			get("user", "/api/positions"),
			get("readonly", "/api/positions?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			get("admin", "/api/positions?deviceId=${st4}&from=2024-07-29T00:00:00Z&to=2024-07-30T00:00:00Z"),
			get("admin", "/api/positions?deviceId=${at2}&from=2018-08-01T00:00:00Z&to=2018-09-01T00:00:00Z"),
			get("admin", "/api/positions/csv?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z").csv(),
			get("admin", "/api/positions/gpx?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			putJSON("admin", "/api/devices/${osmand}/accumulators", `{"deviceId":${osmand},"totalDistance":12345678.9,"hours":3600000}`),
			get("admin", "/api/devices/${osmand}"),
			get("admin", "/api/positions?deviceId=${osmand}"),
		}},

		// How the in-house clients use the server (inventory/clients.md): traccar-bots reads
		// with Basic auth, and both client repositories post OsmAnd positions.
		{Name: "client-usage", Steps: []Step{
			osmand(
				// traccar-bots: POST with the query string only, bearing, UTC timestamp.
				"POST /?id=123456&lat=-23.5299&lon=-46.6099&timestamp=2026-10-01T13:00:00.123Z&speed=1.94&bearing=90&altitude=760&accuracy=8&valid=true\n",
				// track-client-lib (Android/iOS): form body, heading, batt, os, activity.
				"POST /\nid=123456&lat=-23.5298&lon=-46.6098&timestamp=2026-10-01T13:01:00.000Z&speed=3.8876&heading=180.5&accuracy=12.3&altitude=761.2&batt=87.0&charge=true&is_moving=true&odometer=0&activity=in_vehicle&os=android",
				// track-client-lib (JS task): timestamp with a local offset.
				"POST /\nid=123456&lat=-23.5297&lon=-46.6097&timestamp=2026-10-01T10:02:00.000-03:00&speed=0&heading=0&accuracy=5&altitude=760&batt=86.0&charge=false&is_moving=false&odometer=0&activity=still&os=ios",
			),
			basicGet("/api/devices"),
			basicGet("/api/positions"),
			basicGet("/api/groups?all=true"),
			basicGet("/api/geofences?groupId=${fleet}"),
			basicGet("/api/geofences?all=true"),
		}},

		{Name: "reports", Steps: []Step{
			reportGet("/api/reports/route?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/events?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/events?groupId=${fleet}&type=alarm&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/summary?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/summary?deviceId=${osmand}&daily=true&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/trips?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/stops?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/combined?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			reportGet("/api/reports/geofences?deviceId=${osmand}&geofenceId=${home}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			get("admin", "/api/reports/route?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z").
				header("Accept", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet").binary(),
			get("user", "/api/reports/route?deviceId=${st4}&from=2024-07-29T00:00:00Z&to=2024-07-30T00:00:00Z").
				header("Accept", "application/json"),
			get("admin", "/api/reports/route?deviceId=${osmand}&from=2026-10-01T00:00:00Z").header("Accept", "application/json"),
			get("admin", "/api/audit?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
			get("admin", "/api/statistics?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
		}},

		{Name: "commands", Steps: []Step{
			get("admin", "/api/commands/types?deviceId=${st4}"),
			get("admin", "/api/commands/types?protocol=suntech"),
			get("admin", "/api/commands/send?deviceId=${st4}"),
			// No open connection, so the command is queued (tc_commands_queue).
			postJSON("admin", "/api/commands/send", `{"deviceId":${st4},"type":"rebootDevice","attributes":{}}`),
			postJSON("admin", "/api/commands/send", `{"deviceId":${st4},"id":${command}}`),
			postJSON("admin", "/api/commands/send", `{"deviceId":${osmand},"type":"custom","attributes":{"data":"hello"}}`),
			postJSON("readonly", "/api/commands/send", `{"deviceId":${st4},"type":"rebootDevice"}`),
		}},

		{Name: "deletes", Steps: []Step{
			del("admin", "/api/devices/${expiring}"),
			del("admin", "/api/geofences/${zone}"),
			del("admin", "/api/notifications/${alarmNotification}"),
			del("admin", "/api/users/${managed}"),
			del("user", "/api/devices/${osmand}"),
			del("admin", "/api/positions?deviceId=${at3}&from=2018-08-01T00:00:00Z&to=2018-09-01T00:00:00Z"),
			get("admin", "/api/devices"),
			get("admin", "/api/users"),
			del("admin", "/api/session"),
			get("admin", "/api/session"),
		}},
	}
}

func get(actor, path string) Step { return Step{Actor: actor, Method: "GET", Path: path} }
func del(actor, path string) Step { return Step{Actor: actor, Method: "DELETE", Path: path} }

func postWith(actor, path string) Step { return Step{Actor: actor, Method: "POST", Path: path} }

func postJSON(actor, path, js string) Step {
	return Step{Actor: actor, Method: "POST", Path: path, JSON: js}
}

func putJSON(actor, path, js string) Step {
	return Step{Actor: actor, Method: "PUT", Path: path, JSON: js}
}

func delJSON(actor, path, js string) Step {
	return Step{Actor: actor, Method: "DELETE", Path: path, JSON: js}
}

func postForm(actor, path, form string) Step {
	return Step{Actor: actor, Method: "POST", Path: path, Form: form}
}

func login(actor, email, password string) Step {
	return postForm(actor, "/api/session", "email="+strings.ReplaceAll(email, "@", "%40")+"&password="+password)
}

// serverInfo is GET /api/server. For administrators it includes storageSpace, the
// host's live free and total disk space, so only its shape is compared.
func serverInfo(actor string) Step {
	return get(actor, "/api/server").opaque("$.storageSpace")
}

// basicGet is a request as traccar-bots sends it: Basic auth, Accept JSON, no session.
func basicGet(path string) Step {
	return get("", path).header("Authorization", "Basic "+basic("admin@example.com", "secret")).
		header("Accept", "application/json")
}

func reportGet(path string) Step {
	return get("admin", path).header("Accept", "application/json")
}

func (s Step) save(name, path string) Step {
	m := map[string]string{name: path}
	for k, v := range s.Save {
		m[k] = v
	}
	s.Save = m
	return s
}

func (s Step) header(k, v string) Step {
	m := map[string]string{k: v}
	for k2, v2 := range s.Header {
		m[k2] = v2
	}
	s.Header = m
	return s
}

func (s Step) opaqueBody() Step { s.Opaque = append(s.Opaque, "$body"); return s }

func (s Step) opaque(paths ...string) Step {
	s.Opaque = append(s.Opaque, paths...)
	return s
}
func (s Step) binary() Step { s.Binary = true; return s }
func (s Step) csv() Step    { s.CSV = true; return s }
func (s Step) token() Step  { s.Token = true; return s }

func (s Step) unordered(paths ...string) Step {
	s.Unordered = append(s.Unordered, paths...)
	return s
}

func osmand(queries ...string) Step {
	msgs := make([][]byte, len(queries))
	for i, q := range queries {
		msgs[i] = []byte(q)
	}
	return Step{Device: &Traffic{Protocol: "osmand", Transport: "http", Messages: msgs}}
}

func tcp(protocol string, msgs ...[]byte) Step {
	return Step{Device: &Traffic{Protocol: protocol, Transport: "tcp", Messages: msgs}}
}

func udp(protocol string, msgs ...[]byte) Step {
	return Step{Device: &Traffic{Protocol: protocol, Transport: "udp", Messages: msgs}}
}

func text(s string) []byte { return []byte(s) }

// atrackText fills in the length field (third field) of an Atrack text frame. The
// frame decoder trusts it, and the Java test payloads don't carry real lengths because
// unit tests bypass framing. The length counts from the comma after the field to the
// end of the frame, excluding the final '\n'.
func atrackText(s string) []byte {
	parts := strings.SplitN(s, ",", 4)
	rest := "," + strings.TrimSuffix(parts[3], "\n")
	return []byte(parts[0] + "," + parts[1] + "," + strconv.Itoa(len(rest)) + rest + "\n")
}

func hexBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func basic(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

// bigPolygon returns a WKT polygon with n+1 points, about 18 characters per point.
func bigPolygon(n int) string {
	var b strings.Builder
	b.WriteString("POLYGON ((")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.FormatFloat(-23.0-float64(i)*0.001, 'f', -1, 64))
		b.WriteString(" -46.6")
	}
	b.WriteString(", -23 -46.6))")
	return b.String()
}

// calendarData is a weekday 08:00-18:00 (UTC-3) calendar, base64-encoded as Jackson
// encodes byte[].
var calendarData = base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{
	"BEGIN:VCALENDAR",
	"PRODID:-//SIR//compat//EN",
	"VERSION:2.0",
	"BEGIN:VEVENT",
	"UID:compat-business-hours",
	"DTSTAMP:20260101T000000Z",
	"DTSTART:20260105T110000Z",
	"DTEND:20260105T210000Z",
	"RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
	"SUMMARY:Business hours",
	"END:VEVENT",
	"END:VCALENDAR",
	"",
}, "\r\n")))
