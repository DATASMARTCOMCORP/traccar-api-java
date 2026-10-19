package main

import "fmt"

// notifySuite is Step 9's notifications and forwarding: events go to event.forward.url
// (an HTTP sink per side), and notifications through the web (sockets), mail (an SMTP
// sink), SMS (the HTTP sink, as a form template) and command notificators. The drive
// uses timestamps relative to the scenario's start, so events are recent enough to
// notify (notificator.timeThreshold is 15 minutes), except the first part, which is older
// and only forwarded.
func notifySuite() suite {
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret","phone":"+5511999990001"}`).save("admin", "id"),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/users", `{"name":"Viewer","email":"viewer@example.com","password":"secret","phone":"+55 11 99999-0002",
			"attributes":{"language":"pt_BR","timezone":"America/Sao_Paulo","speedUnit":"kmh"}}`).save("viewer", "id"),
		postJSON("admin", "/api/users", `{"name":"Quiet","email":"quiet@example.com","password":"secret"}`).save("quiet", "id"),
		postJSON("admin", "/api/groups", `{"name":"Fleet"}`).save("fleet", "id"),
		postJSON("admin", "/api/devices", `{"name":"Caminhão <1> & 'x'","uniqueId":"300001","groupId":${fleet}}`).save("car", "id"),
		postJSON("admin", "/api/devices", `{"name":"Other","uniqueId":"300002"}`).save("other", "id"),
		postJSON("admin", "/api/permissions", `{"userId":${viewer},"deviceId":${car}}`),
		postJSON("admin", "/api/permissions", `{"userId":${quiet},"groupId":${fleet}}`),

		postJSON("admin", "/api/geofences", `{"name":"Casa é <b>","area":"CIRCLE (-23.5505 -46.6333, 300)"}`).save("home", "id"),
		postJSON("admin", "/api/geofences", `{"name":"Zone","area":"POLYGON ((-23.545 -46.625, -23.545 -46.615, -23.535 -46.615, -23.535 -46.625, -23.545 -46.625))"}`).save("zone", "id"),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${home}}`),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"geofenceId":${zone}}`),
		postJSON("admin", "/api/calendars", `{"name":"Once","data":"QkVHSU46VkNBTEVOREFSClZFUlNJT046Mi4wClBST0RJRDotLy9UcmFjY2FyLy9OT05TR01MIFRyYWNjYXIvL0VOCkJFR0lOOlZFVkVOVApVSUQ6MDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAyCkRUU1RBUlQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI2MTAxM1QwODAwMDAKRFRFTkQ7VFpJRD1BbWVyaWNhL1Nhb19QYXVsbzoyMDI2MTAxM1QwOTAwMDAKU1VNTUFSWTpFdmVudApFTkQ6VkVWRU5UCkVORDpWQ0FMRU5EQVI="}`).save("once", "id"),
		postJSON("admin", "/api/commands", `{"description":"Ping","type":"custom","attributes":{"data":"ping"}}`).save("ping", "id"),

		// Geofence entries and exits by web, mail and SMS, for the admin and the viewer
		// (linked to the device); the group's user isn't linked to the notification.
		postJSON("admin", "/api/notifications", `{"type":"geofenceEnter","notificators":"web,mail,sms"}`).save("enter", "id"),
		postJSON("admin", "/api/permissions", `{"deviceId":${car},"notificationId":${enter}}`),
		postJSON("admin", "/api/permissions", `{"userId":${viewer},"notificationId":${enter}}`),
		// Only for the zone, through the group.
		postJSON("admin", "/api/notifications", `{"type":"geofenceExit","notificators":"mail","attributes":{"geofenceIds":"${zone}"}}`).save("exit", "id"),
		postJSON("admin", "/api/permissions", `{"groupId":${fleet},"notificationId":${exit}}`),
		// Motion by SMS for all of the admin's devices (always).
		postJSON("admin", "/api/notifications", `{"type":"deviceMoving","notificators":"sms","always":true}`),
		postJSON("admin", "/api/notifications", `{"type":"deviceStopped","notificators":"sms , web","always":true}`),
		// Alarms: only sos, on the web.
		postJSON("admin", "/api/notifications", `{"type":"alarm","notificators":"web","always":true,"attributes":{"alarms":"sos,powerCut"}}`),
		// Overspeed through the saved command (queued: OsmAnd has no live commands).
		postJSON("admin", "/api/notifications", `{"type":"deviceOverspeed","notificators":"command","always":true,"commandId":${ping}}`),
		// A calendar that never matches now.
		postJSON("admin", "/api/notifications", `{"type":"ignitionOn","notificators":"web","always":true,"calendarId":${once}}`),
		// A notificator type that isn't enabled: the rest of the event's notifications fail.
		postJSON("admin", "/api/notifications", `{"type":"ignitionOff","notificators":"web,telegram","always":true}`),
		putJSON("admin", "/api/devices/${car}", `{"id":${car},"name":"Caminhão <1> & 'x'","uniqueId":"300001","groupId":${fleet},"attributes":{"speedLimit":30}}`),
	}
	config := map[string]string{
		"server.buffering.threshold": "0",
		"event.forward.url":          "${sinkHTTP}/forward",
		"event.forward.header":       "X-Source: sir\nAuthorization: Bearer forward-token",
		"sms.http.url":               "${sinkHTTP}/sms",
		"sms.http.template":          "to={phone}&text={message}&from=SIR",
		"mail.smtp.host":             "127.0.0.1",
		"mail.smtp.port":             "${sinkSMTP}",
		"mail.smtp.from":             "alerts@example.com",
		"mail.smtp.fromName":         "SIR Rastreamento",
		"mail.smtp.username":         "alerts",
		"mail.smtp.password":         "secret",
	}

	fix := func(min int, lat, lon, speed float64, extra string) string {
		return fmt.Sprintf("/?id=300001&lat=%v&lon=%v&timestamp=${now:%d}&speed=%v%s", lat, lon, min*60, speed, extra)
	}
	read := func() []Step {
		return []Step{
			{Socket: &Socket{Name: "a", Read: true}, Unordered: []string{"$"}},
			{Socket: &Socket{Name: "v", Read: true}, Unordered: []string{"$"}},
			{Sink: true, Unordered: []string{"$"}},
		}
	}
	steps := []Step{
		login("admin", "admin@example.com", "secret"),
		login("viewer", "viewer@example.com", "secret"),
		{Actor: "admin", Socket: &Socket{Name: "a", Open: true}},
		{Actor: "viewer", Socket: &Socket{Name: "v", Open: true}},
	}
	steps = append(steps, read()...)
	// An hour ago: forwarded, too old to notify.
	steps = append(steps, osmand(
		fix(-60, -23.5505, -46.6333, 0, "&ignition=false"),
		fix(-59, -23.5600, -46.6400, 40, "&alarm=sos"),
	))
	steps = append(steps, read()...)
	// Recent: parked at home, ignition on, out through the zone (speeding, alarms), away,
	// then parked long enough for a stop.
	steps = append(steps, osmand(
		fix(-16, -23.5505, -46.6333, 0, "&ignition=false"),
		fix(-15, -23.5505, -46.6333, 0, "&ignition=true"),
		fix(-14, -23.5480, -46.6300, 20, "&ignition=true"),
		fix(-13, -23.5400, -46.6200, 35, "&ignition=true&alarm=sos,overspeed"),
	))
	steps = append(steps, read()...)
	steps = append(steps, osmand(
		fix(-12, -23.5380, -46.6180, 30, "&ignition=true&alarm=lowBattery"),
		fix(-11, -23.5300, -46.6100, 30, "&ignition=true"),
		fix(-10, -23.5200, -46.6000, 30, "&ignition=true"),
		fix(-8, -23.5100, -46.5900, 25, "&ignition=true"),
		fix(-7, -23.5050, -46.5850, 0, "&ignition=false"),
		fix(-4, -23.5050, -46.5850, 0, "&ignition=false"),
		fix(-1, -23.5050, -46.5851, 0, "&ignition=false"),
	))
	steps = append(steps, read()...)
	// Password reset mails (with a token) and test notifications.
	steps = append(steps,
		postForm("", "/api/password/reset", "email=VIEWER%40Example.com"),
		postForm("", "/api/password/reset", "email=nobody%40example.com"),
		postForm("", "/api/password/reset", "other=1"),
		postWith("admin", "/api/notifications/test/web"),
		postWith("admin", "/api/notifications/test/mail"),
		postWith("viewer", "/api/notifications/test/sms"),
		postWith("viewer", "/api/notifications/test/mail"),
		postWith("admin", "/api/notifications/test/command"),
		postWith("admin", "/api/notifications/test/telegram"),
		postWith("admin", "/api/notifications/test/unknown"),
		// Commands by SMS: custom ones as they are, others only where the protocol of
		// the latest position encodes them (OsmAnd doesn't); a device without a phone
		// fails.
		postJSON("admin", "/api/commands/send", `{"deviceId":${car},"type":"custom","textChannel":true,"attributes":{"data":"STATUS#"}}`),
		postJSON("admin", "/api/commands/send", `{"deviceId":${other},"type":"custom","textChannel":true,"attributes":{"data":"PING"}}`),
		putJSON("admin", "/api/devices/${other}", `{"id":${other},"name":"Other","uniqueId":"300002","phone":"+5511988887777"}`),
		postJSON("admin", "/api/commands/send", `{"deviceId":${other},"type":"custom","textChannel":true,"attributes":{"data":"PING"}}`),
		postJSON("admin", "/api/commands/send", `{"deviceId":${other},"type":"custom","textChannel":true}`),
		postJSON("admin", "/api/commands/send", `{"deviceId":${other},"type":"rebootDevice","textChannel":true}`),
		postJSON("admin", "/api/commands/send", `{"deviceId":${car},"type":"rebootDevice","textChannel":true}`),
	)
	steps = append(steps, read()...)
	steps = append(steps,
		get("admin", "/api/commands/send?deviceId=${car}"),
		Step{Socket: &Socket{Name: "a", Close: true}},
		Step{Socket: &Socket{Name: "v", Close: true}},
		// Every notificator: Java's order is a hash set's, so only the answer is compared
		// (command fails without a notification, whatever was sent before).
		postWith("admin", "/api/notifications/test"),
	)
	return suite{seed: seed, strictSeed: true, config: config, scenarios: []Scenario{
		{Name: "notify-drive", Steps: steps},
	}}
}
