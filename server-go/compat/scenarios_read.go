package main

// readSuite is Step 4: every read-only endpoint, per role. The seed is the full library
// (everything but the final deletes) run through Java, so the database holds users of
// every role, groups, devices, geofences, permissions, and positions and events from
// real device traffic. The scenarios only read.
func readSuite() suite {
	var seed []Step
	for _, sc := range library() {
		if sc.Name != "deletes" {
			seed = append(seed, sc.Steps...)
		}
	}
	seed = append(seed,
		login("seedadmin", "admin@example.com", "secret"),
		get("seedadmin", "/api/reports/events?deviceId=${osmand}&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z").
			header("Accept", "application/json").save("event", "0.id"),
		// A four-level group chain with a device at each level, and a user linked only to
		// the top group. Java's permission SQL follows groups up to the grandparent, so
		// the deepest device is not visible to that user.
		postJSON("seedadmin", "/api/groups", `{"name":"L1"}`).save("l1", "id"),
		postJSON("seedadmin", "/api/groups", `{"name":"L2","groupId":${l1}}`).save("l2", "id"),
		postJSON("seedadmin", "/api/groups", `{"name":"L3","groupId":${l2}}`).save("l3", "id"),
		postJSON("seedadmin", "/api/groups", `{"name":"L4","groupId":${l3}}`).save("l4", "id"),
		postJSON("seedadmin", "/api/devices", `{"name":"In L2","uniqueId":"DEEP-2","groupId":${l2}}`).save("dl2", "id"),
		postJSON("seedadmin", "/api/devices", `{"name":"In L3","uniqueId":"DEEP-3","groupId":${l3}}`).save("dl3", "id"),
		postJSON("seedadmin", "/api/devices", `{"name":"In L4","uniqueId":"DEEP-4","groupId":${l4}}`).save("dl4", "id"),
		postJSON("seedadmin", "/api/geofences", `{"name":"Deep zone","area":"CIRCLE (-23.5 -46.6, 100)"}`).save("dz", "id"),
		postJSON("seedadmin", "/api/permissions", `{"groupId":${l3},"geofenceId":${dz}}`),
		postJSON("seedadmin", "/api/users", `{"name":"Deep","email":"deep@example.com","password":"deep"}`).save("deep", "id"),
		postJSON("seedadmin", "/api/permissions", `{"userId":${deep},"groupId":${l1}}`),
	)

	day := "from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"
	// collections are the generic resources: path, a linked object, and whether it's an
	// "extended" resource (groupId/deviceId filters).
	collections := []struct {
		path, item string
		extended   bool
	}{
		{"/api/groups", "${fleet}", false},
		{"/api/calendars", "${calendar}", false},
		{"/api/orders", "${order}", false},
		{"/api/reports", "${report}", false},
		{"/api/geofences", "${home}", true},
		{"/api/notifications", "${notification}", true},
		{"/api/drivers", "${driver}", true},
		{"/api/attributes/computed", "${computed}", true},
		{"/api/commands", "${command}", true},
		{"/api/maintenance", "${maintenance}", true},
	}
	var generic []Step
	for _, c := range collections {
		generic = append(generic,
			get("admin", c.path),
			get("admin", c.path+"?all=true"),
			get("admin", c.path+"?userId=${user}"),
			get("admin", c.path+"?excludeAttributes=true&limit=1&offset=0"),
			get("admin", c.path+"?keyword=a"),
			get("admin", c.path+"/"+c.item),
			get("admin", c.path+"/99999"),
			get("user", c.path),
			get("user", c.path+"?all=true"),
			get("user", c.path+"/"+c.item),
			get("manager", c.path),
			get("manager", c.path+"?userId=${user}"),
		)
		if c.extended {
			generic = append(generic,
				get("admin", c.path+"?groupId=${fleet}"),
				get("admin", c.path+"?deviceId=${osmand}"),
				get("user", c.path+"?deviceId=${osmand}"),
				get("user", c.path+"?deviceId=${st4}"), // not visible to the user: 400
			)
		}
	}

	logins := []Step{
		login("admin", "admin@example.com", "secret"),
		login("manager", "manager@example.com", "m"),
		login("user", "user@example.com", "u"),
		login("readonly", "readonly@example.com", "r"),
	}

	return suite{seed: seed, scenarios: []Scenario{
		{Name: "login", Steps: logins},
		{Name: "devices", Steps: []Step{
			get("admin", "/api/devices"),
			get("admin", "/api/devices?all=true"),
			get("admin", "/api/devices?userId=${user}"),
			get("admin", "/api/devices?uniqueId=123456&uniqueId=807587388&uniqueId=nope"),
			get("admin", "/api/devices?id=${osmand}&id=${st4}"),
			get("admin", "/api/devices?deviceId=${osmand}"), // linked devices
			get("admin", "/api/devices?keyword=SUN"),
			get("admin", "/api/devices?limit=2&offset=1"),
			get("admin", "/api/devices?excludeAttributes=true"),
			get("admin", "/api/devices/${osmand}"),
			get("admin", "/api/devices/99999"),
			get("admin", "/api/devices/abc"),
			get("admin", "/api/devices?userId=abc"),
			get("manager", "/api/devices"),
			get("manager", "/api/devices?all=true"),
			get("manager", "/api/devices?userId=${user}"),
			get("manager", "/api/devices/${osmand}"),
			get("user", "/api/devices"),
			get("user", "/api/devices?userId=${admin}"),
			get("user", "/api/devices/${st4}"),
			get("readonly", "/api/devices"),
			get("", "/api/devices"),
		}},
		{Name: "users", Steps: []Step{
			get("admin", "/api/users"),
			get("admin", "/api/users?userId=${manager}"),
			get("admin", "/api/users?deviceId=${osmand}"),
			get("admin", "/api/users?keyword=MAN&excludeAttributes=true"),
			get("admin", "/api/users/${user}"),
			get("manager", "/api/users"),
			get("manager", "/api/users/${user}"),
			get("manager", "/api/users/${admin}"),
			get("user", "/api/users"),
			get("user", "/api/users/${user}"),
			get("user", "/api/users?deviceId=${osmand}"), // not a manager: 400
		}},
		{Name: "collections", Steps: generic},
		{Name: "group-nesting", Steps: []Step{
			login("deep", "deep@example.com", "deep"),
			get("deep", "/api/groups"),
			get("deep", "/api/devices"),
			get("deep", "/api/devices/${dl3}"),
			get("deep", "/api/devices/${dl4}"),
			get("deep", "/api/geofences"),
			get("deep", "/api/geofences?deviceId=${dl3}"),
			get("admin", "/api/geofences?groupId=${l3}"),
			get("admin", "/api/devices?userId=${deep}"),
		}},
		{Name: "positions-and-events", Steps: []Step{
			get("admin", "/api/positions"),
			get("admin", "/api/positions?deviceId=${osmand}"),
			get("admin", "/api/positions?deviceId=${osmand}&"+day),
			get("admin", "/api/positions?deviceId=${st4}&from=2024-07-29T00:00:00Z&to=2024-07-30T00:00:00Z"),
			get("admin", "/api/positions?id=${lastPosition}"),
			get("admin", "/api/positions?id=99999"),
			get("admin", "/api/positions?deviceId=${osmand}&from=yesterday&to=today"),
			get("user", "/api/positions"),
			get("user", "/api/positions?deviceId=${osmand}&"+day),
			get("user", "/api/positions?deviceId=${st4}"),
			get("readonly", "/api/positions?deviceId=${osmand}&"+day),
			get("admin", "/api/events/${event}"),
			get("user", "/api/events/${event}"),
			get("readonly", "/api/events/${event}"),
			get("admin", "/api/events/99999"),
		}},
		{Name: "admin-listings", Steps: []Step{
			get("admin", "/api/statistics?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
			get("admin", "/api/statistics"),
			get("user", "/api/statistics?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
			get("admin", "/api/audit?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
			get("user", "/api/audit?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"),
			get("admin", "/api/permissions?userId=${user}&deviceId=0"),
			get("admin", "/api/permissions?deviceId=${osmand}&geofenceId=0"),
			get("admin", "/api/permissions?groupId=${fleet}&geofenceId=${zone}"),
			get("user", "/api/permissions?userId=${user}&deviceId=0"),
			get("admin", "/api/server/gc"),
			get("user", "/api/server/gc"),
		}},
		{Name: "types-and-misc", Steps: []Step{
			get("admin", "/api/commands/types"),
			get("admin", "/api/commands/types?deviceId=${st4}"),
			get("admin", "/api/commands/types?deviceId=${st4}&textChannel=true"),
			get("admin", "/api/commands/types?deviceId=${osmand}"),
			get("admin", "/api/commands/types?deviceId=${expiring}"), // no position: custom only
			get("admin", "/api/commands/send?deviceId=${st4}"),
			get("admin", "/api/commands/send?deviceId=${osmand}"),
			get("user", "/api/commands/types?deviceId=${st4}"),
			get("admin", "/api/notifications/types"),
			get("admin", "/api/notifications/notificators").unordered("$"),
			get("admin", "/api/notifications/notificators?announcement=true").unordered("$"),
			get("admin", "/api/server/timezones"),
			get("", "/api/health"),
			get("", "/api/health").header("Accept", "application/json"),
		}},
	}}
}
