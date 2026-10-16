package main

// liveSuite is Step 8: /api/socket. Sockets stay open across device traffic and API
// calls, and each read compares the messages received since the previous one: the
// latest positions on open, then devices (status), positions and, once a client asks for
// them, raw device logs. Within a read, Java's order is a race between the decoder
// thread (device status, logs) and position processing, so reads compare as multisets.
func liveSuite() suite {
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`).save("admin", "id"),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/users", `{"name":"Viewer","email":"viewer@example.com","password":"secret"}`).save("viewer", "id"),
		postJSON("admin", "/api/devices", `{"name":"Phone","uniqueId":"200001"}`).save("phone", "id"),
		postJSON("admin", "/api/devices", `{"name":"Shared","uniqueId":"200002"}`).save("shared", "id"),
		postJSON("admin", "/api/devices", `{"name":"Suntech","uniqueId":"907131077"}`).save("st", "id"),
		postJSON("admin", "/api/permissions", `{"userId":${viewer},"deviceId":${shared}}`),
		postForm("admin", "/api/session/token", "expiration=2035-01-01T00:00:00Z").save("token", "$body"),
		postForm("admin", "/api/session/token", "expiration=2000-01-01T00:00:00Z").save("expired", "$body"),
	}
	config := map[string]string{
		"server.buffering.threshold": "0",
		"web.showUnknownDevices":     "true",
	}
	open := func(actor, name, query string) Step {
		return Step{Actor: actor, Socket: &Socket{Name: name, Open: true, Query: query}}
	}
	read := func(name string) Step {
		return Step{Socket: &Socket{Name: name, Read: true}, Unordered: []string{"$", "$[*].positions"}}
	}
	send := func(name, msg string) Step { return Step{Socket: &Socket{Name: name, Send: msg}} }
	closeSocket := func(name string) Step { return Step{Socket: &Socket{Name: name, Close: true}} }
	st := tcp("suntech", text("ST300STT;907131077;04;706;20190227;23:59:34;cc719;-12.963490;-038.499587;000.067;000.00;7;1;57095;12.50;000000;1;0337;000207;0.0;1;0;012E717F010000;1\r"))

	return suite{seed: seed, strictSeed: true, config: config, scenarios: []Scenario{
		{Name: "socket-handshake", Steps: []Step{
			// No user: no socket.
			open("", "anon", ""),
			open("", "bad", "token=bad"),
			open("", "expired", "token=${expired}"),
			// A token, then a session: the latest positions (none yet).
			open("", "tok", "token=${token}"), read("tok"), closeSocket("tok"),
			login("viewer", "viewer@example.com", "secret"),
			open("viewer", "sess", ""), read("sess"), closeSocket("sess"),
			// A plain request to the socket path.
			login("admin", "admin@example.com", "secret"),
			get("admin", "/api/socket"),
		}},

		{Name: "socket-updates", Steps: []Step{
			login("admin", "admin@example.com", "secret"),
			login("viewer", "viewer@example.com", "secret"),
			open("admin", "a", ""), read("a"),
			open("viewer", "v", ""), read("v"),
			// Status and positions; the viewer sees only the shared device.
			osmand("/?id=200001&lat=-23.5&lon=-46.6&timestamp=2026-10-16T10:00:00Z&speed=10"),
			read("a"), read("v"),
			osmand("/?id=200002&lat=-23.6&lon=-46.7&timestamp=2026-10-16T10:01:00Z"),
			read("a"), read("v"),
			// An older fix isn't the latest: no position message.
			osmand("/?id=200002&lat=-23.61&lon=-46.71&timestamp=2026-10-16T09:00:00Z"),
			read("a"), read("v"),
			// Logs once asked for: raw reads of HTTP and TCP devices, and of unknown ones
			// (web.showUnknownDevices).
			send("a", `{"logs":true}`), send("v", `{"logs":"true"}`),
			osmand("/?id=200002&lat=-23.62&lon=-46.72&timestamp=2026-10-16T10:02:00Z"),
			st,
			osmand("/?id=999999&lat=1&lon=2"),
			read("a"), read("v"),
			send("a", `{"logs":false}`), send("v", `{"other":1}`),
			osmand("/?id=200002&lat=-23.63&lon=-46.73&timestamp=2026-10-16T10:03:00Z"),
			read("a"), read("v"),
			send("v", `not json`), send("v", `{"logs":null}`),
			// The accumulators store a new latest position.
			putJSON("admin", "/api/devices/${phone}/accumulators", `{"deviceId":${phone},"totalDistance":1000.5,"hours":3600000}`),
			read("a"),
			// A device the admin creates reaches the admin's sockets.
			postJSON("admin", "/api/devices", `{"name":"New","uniqueId":"200003"}`).save("fresh", "id"),
			osmand("/?id=200003&lat=-23.7&lon=-46.8&timestamp=2026-10-16T10:04:00Z"),
			read("a"), read("v"),
			// A device update reloads the cached device (its positionId).
			putJSON("admin", "/api/devices/${phone}", `{"id":${phone},"name":"Phone 2","uniqueId":"200001"}`),
			osmand("/?id=200001&lat=-23.51&lon=-46.61&timestamp=2026-10-16T10:05:00Z"),
			read("a"),
			closeSocket("a"), closeSocket("v"),
		}},

		{Name: "socket-reopen", Steps: []Step{
			// Latest positions on open, for the devices the user sees.
			login("admin", "admin@example.com", "secret"),
			open("admin", "a", ""), read("a"),
			login("viewer", "viewer@example.com", "secret"),
			open("viewer", "v", ""), read("v"),
			closeSocket("a"), closeSocket("v"),
		}},
	}}
}
