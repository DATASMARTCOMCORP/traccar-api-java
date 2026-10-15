package main

// writeSuite is the rest of Step 5, on the Java-seeded database of the read suite:
// accumulators, image upload, sharing, password update, position deletes, the user
// privilege rules, server updates, group cycles, non-admin permission links, group
// commands, and how request bodies are read (coercions, unknown properties, nulls).
func writeSuite() suite {
	seed := readSuite().seed
	seed = append(seed,
		login("seeduser", "user@example.com", "u"),
		postForm("seeduser", "/api/session/token", "expiration=2035-01-01T00:00:00Z").save("userToken", "$body"),
		postJSON("seedadmin", "/api/users", `{"name":"Limited","email":"limited@example.com","password":"l","deviceLimit":1}`).
			save("limited", "id"),
		postJSON("seedadmin", "/api/users", `{"name":"Fixed","email":"fixed@example.com","password":"f","fixedEmail":true}`).
			save("fixed", "id"),
		postJSON("seedadmin", "/api/users", `{"name":"Small manager","email":"small@example.com","password":"s","userLimit":1}`).
			save("small", "id"),
	)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	return suite{seed: seed, scenarios: []Scenario{
		{Name: "login", Steps: []Step{
			login("admin", "admin@example.com", "secret"),
			login("manager", "manager@example.com", "m"),
			login("user", "user@example.com", "u"),
			login("readonly", "readonly@example.com", "r"),
			login("limited", "limited@example.com", "l"),
			login("fixed", "fixed@example.com", "f"),
			login("small", "small@example.com", "s"),
		}},
		{Name: "accumulators-and-media", Steps: []Step{
			putJSON("admin", "/api/devices/${osmand}/accumulators", `{"deviceId":${osmand},"totalDistance":"1000.5","hours":7200000}`),
			putJSON("admin", "/api/devices/${osmand}/accumulators", `{"deviceId":${osmand},"hours":null}`),
			putJSON("admin", "/api/devices/${expiring}/accumulators", `{"deviceId":${expiring},"totalDistance":1}`), // no position: 400
			putJSON("readonly", "/api/devices/${st4}/accumulators", `{"deviceId":${st4},"totalDistance":1}`),
			putJSON("admin", "/api/devices/${osmand}/accumulators", `{"deviceId":${osmand},"odometer":1}`), // unknown property: 400
			get("admin", "/api/positions?deviceId=${osmand}"),
			Step{Actor: "admin", Method: "POST", Path: "/api/devices/${osmand}/image", Raw: png, RawType: "image/png"},
			Step{Actor: "admin", Method: "POST", Path: "/api/devices/${osmand}/image", Raw: png, RawType: "image/bmp"},
			Step{Actor: "admin", Method: "POST", Path: "/api/devices/${osmand}/image", Raw: png, RawType: "text/plain"},
			Step{Actor: "user", Method: "POST", Path: "/api/devices/${st4}/image", Raw: png, RawType: "image/png"},
			Step{Actor: "readonly", Method: "POST", Path: "/api/devices/${st4}/image", Raw: png, RawType: "image/png"},
		}},
		{Name: "sharing-and-passwords", Steps: []Step{
			postForm("user", "/api/share/device", "deviceId=${osmand}&expiration=2030-01-01T00:00:00Z").save("share", "$body").token(),
			postForm("user", "/api/share/device", "deviceId=${osmand}&expiration=2031-01-01T00:00:00Z").token(), // reuses the share user
			postForm("user", "/api/share/device", "deviceId=${st4}&expiration=2030-01-01T00:00:00Z"),            // not visible: 400
			postForm("admin", "/api/share/group", "groupId=${fleet}&expiration=2030-01-01T00:00:00Z").token(),
			get("shared", "/api/session?token=${share}"),
			get("shared", "/api/devices"),
			putJSON("shared", "/api/devices/${osmand}", `{"id":${osmand},"name":"x","uniqueId":"123456"}`), // read-only: 400
			postForm("shared", "/api/share/device", "deviceId=${osmand}&expiration=2030-01-01T00:00:00Z"),  // temporary: 400
			postForm("", "/api/password/update", "token=${userToken}&password=newsecret"),
			login("user2", "user@example.com", "newsecret"),
			postForm("", "/api/password/update", "token=garbage&password=x"),
			postForm("", "/api/password/update", "token=${userToken}"), // no password: unchanged
			login("user3", "user@example.com", "newsecret"),
		}},
		{Name: "permissions-and-commands", Steps: []Step{
			postJSON("manager", "/api/permissions", `{"userId":${managed},"deviceId":${st4}}`),
			postJSON("manager", "/api/permissions", `{"userId":${user},"deviceId":${st4}}`),
			postJSON("manager", "/api/permissions", `{"userId":${manager},"deviceId":${osmand}}`), // not visible: 400
			postJSON("readonly", "/api/permissions", `{"userId":${readonly},"deviceId":${st4}}`),
			postJSON("admin", "/api/permissions", `{"userId":${user},"deviceId":${st4},"extra":1}`),
			postJSON("admin", "/api/permissions", `{"userid":${user},"DEVICEID":${stt}}`), // keys are case-insensitive
			postJSON("admin", "/api/permissions", `{"userId":${user},"deviceId":null}`),
			postJSON("admin", "/api/permissions", `{"userId":${user},"widgetId":1}`),
			postJSON("admin", "/api/permissions", `{"userId":${user},"deviceId":${stt}}`), // duplicate link row
			delJSON("admin", "/api/permissions/bulk", `[{"userId":${user},"deviceId":${stt}},{"userId":${user},"deviceId":${st4}}]`),
			get("admin", "/api/permissions?userId=${user}&deviceId=0"),
			// One device in the group: with several, Java queues them in a different order on
			// every run (a HashSet of identity-hashed devices).
			postJSON("admin", "/api/commands/send?groupId=${north}", `{"type":"rebootDevice","attributes":{}}`),
			postJSON("admin", "/api/commands/send", `{"deviceId":${st300},"type":"custom","attributes":{"data":"x","noQueue":true}}`),
			postJSON("admin", "/api/commands/send", `{"deviceId":${st300},"type":"custom","textChannel":true,"attributes":{"data":"x"}}`),
			postJSON("admin", "/api/commands/send", `{"id":${command},"deviceId":${stt}}`),
			postJSON("user", "/api/commands/send", `{"deviceId":${st4},"type":"custom"}`),
			del("admin", "/api/positions/${lastPosition}"),
			del("admin", "/api/positions/99999"),
			del("readonly", "/api/positions?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
			del("admin", "/api/positions?deviceId=${osmand}"),
			del("admin", "/api/positions?deviceId=${osmand}&from=2026-10-01T12:20:00Z&to=2026-10-02T00:00:00Z"),
			get("admin", "/api/positions?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"),
		}},
		{Name: "users", Steps: []Step{
			// A user editing themselves, read-only rules, and privilege escalation.
			putJSON("user2", "/api/users/${user}", `{"id":${user},"name":"User again","email":"user@example.com","deviceLimit":10,"attributes":{"speedUnit":"kmh","timezone":"America/Sao_Paulo"}}`),
			putJSON("user2", "/api/users/${user}", `{"id":${user},"name":"User again","email":"user@example.com","deviceLimit":99}`),
			putJSON("user2", "/api/users/${user}", `{"id":${user},"name":"User again","email":"user@example.com","deviceLimit":10,"administrator":true}`),
			putJSON("user2", "/api/users/${user}", `{"id":${user},"name":"User again","email":"user@example.com","deviceLimit":10,"readonly":true}`),
			putJSON("readonly", "/api/users/${readonly}", `{"id":${readonly},"name":"Readonly","email":"readonly@example.com","readonly":true}`),
			putJSON("readonly", "/api/users/${readonly}", `{"id":${readonly},"name":"Readonly","email":"readonly@example.com","readonly":true,"attributes":{"notificationTokens":"abc"}}`),
			putJSON("fixed", "/api/users/${fixed}", `{"id":${fixed},"name":"Fixed","email":"other@example.com","fixedEmail":true}`),
			putJSON("admin", "/api/users/${fixed}", `{"id":${fixed},"name":"Fixed","email":"other@example.com","fixedEmail":false,"password":"f2"}`),
			login("fixed2", "other@example.com", "f2"),
			putJSON("manager", "/api/users/${managed}", `{"id":${managed},"name":"Managed","email":"managed@example.com","disabled":true}`),
			putJSON("manager", "/api/users/${user}", `{"id":${user},"name":"Hijack","email":"user@example.com"}`),
			postJSON("small", "/api/users", `{"name":"First","email":"first@example.com","password":"x"}`),
			postJSON("small", "/api/users", `{"name":"Second","email":"second@example.com","password":"x"}`), // limit 1: 400
			postJSON("small", "/api/users", `{"name":"Admin?","email":"third@example.com","administrator":true}`),
			postJSON("", "/api/users", `{"name":"Anon","email":"anon@example.com","password":"x"}`), // registration closed
			postJSON("admin", "/api/users", `{"email":"noname@example.com"}`),                       // name is NOT NULL: 400
			postJSON("admin", "/api/users", `{"name":"No email"}`),
			postJSON("admin", "/api/users", `{"name":"Typo","email":"typo@example.com","emial":"x"}`), // unknown property
			postJSON("admin", "/api/users", `{"name":"Coerced","email":"coerced@example.com","deviceLimit":"7","zoom":3.9,"readonly":"false","latitude":"1.5"}`),
			del("admin", "/api/users/${managed}"),
			del("small", "/api/users/${small}"),
			get("small", "/api/session"),
		}},
		{Name: "entities", Steps: []Step{
			postJSON("limited", "/api/devices", `{"name":"One","uniqueId":"LIM-1"}`),
			postJSON("limited", "/api/devices", `{"name":"Two","uniqueId":"LIM-2"}`), // deviceLimit 1: 400
			postJSON("readonly", "/api/devices", `{"name":"RO","uniqueId":"RO-1"}`),
			postJSON("admin", "/api/devices", `{"name":"Spaces","uniqueId":"  SP-1  ","phone":" 123 ","status":"online","positionId":5}`),
			postJSON("admin", "/api/devices", `{"name":"No unique id"}`),
			postJSON("admin", "/api/devices", `{"name":"Null unique id","uniqueId":null}`),
			postJSON("admin", "/api/devices", `{"uniqueId":"NO-NAME"}`),
			postJSON("admin", "/api/devices", `{"name":"Ignored","uniqueId":"IGN-1","motionState":true,"overspeedTime":"2020-01-01T00:00:00Z"}`),
			putJSON("admin", "/api/groups/${l1}", `{"id":${l1},"name":"L1","groupId":${l3}}`), // cycle: 400
			putJSON("admin", "/api/groups/${l1}", `{"id":${l1},"name":"L1","groupId":${l2}}`),
			putJSON("admin", "/api/groups/${fleet}", `{"id":${fleet},"name":"Fleet renamed"}`),
			putJSON("user2", "/api/devices/${osmand}", `{"id":${osmand},"name":"Phone","uniqueId":"123456","groupId":${l1}}`), // group not visible
			putJSON("admin", "/api/devices/99999", `{"id":99999,"name":"Ghost","uniqueId":"GHOST"}`),                          // missing row: Java answers 200
			putJSON("admin", "/api/devices/abc", `{"id":1,"name":"x","uniqueId":"x"}`),
			postJSON("admin", "/api/calendars", `{"name":"Bad","data":"bm90IGEgY2FsZW5kYXI="}`),
			postJSON("admin", "/api/commands", `{"description":"Custom","type":"custom","attributes":{"data":"x"},"unknown":1}`), // ignoreUnknown
			postJSON("admin", "/api/attributes/computed", `{"description":"x","attribute":"y","expression":"1","type":"number","attributes":{}}`),
			postJSON("admin", "/api/geofences", `{"name":"Typed","area":"CIRCLE (1 2, 3)","calendarId":"${calendar}"}`),
			putJSON("admin", "/api/server", `{"id":1,"registration":false,"map":"osm","zoom":"12","attributes":{"totpEnable":true},"version":"9.9","emailEnabled":true}`),
			postWith("", "/api/users/totp").opaqueBody(),
			putJSON("user", "/api/server", `{"id":1}`),
			del("readonly", "/api/devices/${st4}"),
			del("admin", "/api/geofences/99999"),
		}},
	}}
}
