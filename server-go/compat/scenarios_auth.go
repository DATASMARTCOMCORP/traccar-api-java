package main

import "encoding/base64"

// A suite is a seed plus scenarios. The seed runs once through Java on the template
// database, before both sides are cloned from it, so both start with the same
// Java-created rows. Its variables are visible to every scenario.
type suite struct {
	seed      []Step
	scenarios []Scenario
	// strictSeed fails the run when a seed step answers >= 300.
	strictSeed bool
	// config entries override the targets' defaults (see target.writeConfig).
	config map[string]string
}

func suites() map[string]suite {
	return map[string]suite{
		"full":     {scenarios: library()},
		"auth":     authSuite(),
		"read":     readSuite(),
		"crud":     crudSuite(),
		"write":    writeSuite(),
		"ingest":   ingestSuite(),
		"buffered": bufferedSuite(),
		"status":   statusSuite(),
	}
}

// crudSuite is the full library from an empty database without the scenarios that need
// device traffic (Go gets protocol ports in Steps 6 and 7): registration, users, tokens,
// every entity's create/update/delete, permissions, command queueing and deletes.
func crudSuite() suite {
	var scenarios []Scenario
	for _, sc := range library() {
		if sc.Name != "device-traffic" && sc.Name != "reports" && sc.Name != "client-usage" {
			scenarios = append(scenarios, sc)
		}
	}
	return suite{scenarios: scenarios}
}

// totpSecret is a fixed base32 TOTP key; ${totp:...} expands to its current code.
const totpSecret = "JBSWY3DPEHPK3PXP"

// authSuite is Step 3: logins, sessions, Basic auth and bearer tokens. Users and tokens
// are created by Java in the seed, so the scenarios need nothing but the session API.
// Comments note the Java behavior each step pins down.
func authSuite() suite {
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`).save("admin", "id"),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/users", `{"name":"User","email":"user@example.com","password":"u","attributes":{"speedUnit":"kmh"}}`).save("user", "id"),
		postJSON("admin", "/api/users", `{"name":"Disabled","email":"disabled@example.com","password":"d","disabled":true}`).save("disabled", "id"),
		postJSON("admin", "/api/users", `{"name":"Expired","email":"expired@example.com","password":"e","expirationTime":"2020-01-01T00:00:00.000+00:00"}`),
		postJSON("admin", "/api/users", `{"name":"Totp","email":"totp@example.com","password":"t","totpKey":"`+totpSecret+`"}`),
		postJSON("admin", "/api/users", `{"name":"Login","email":"login-user@example.com","login":"LoginName","password":"l"}`),
		postJSON("admin", "/api/users", `{"name":"Manager","email":"manager@example.com","password":"m","userLimit":5}`).save("manager", "id"),
		login("manager", "manager@example.com", "m"),
		postJSON("manager", "/api/users", `{"name":"Managed","email":"managed@example.com","password":"x"}`).save("managed", "id"),
		// Tokens signed by Java (this also creates the key pair in tc_keystore).
		postForm("admin", "/api/session/token", "expiration=2035-01-01T00:00:00Z").save("javaToken", "$body"),
		postForm("admin", "/api/session/token", "expiration=2000-01-01T00:00:00Z").save("expiredToken", "$body"),
		postForm("admin", "/api/session/token", "expiration=2035-01-01T00:00:00Z").save("revokedToken", "$body"),
		postForm("admin", "/api/session/token/revoke", "token=${revokedToken}"),
	}
	bearer := func(s Step, token string) Step { return s.header("Authorization", "Bearer "+token) }
	basicAs := func(s Step, user, password string) Step {
		return s.header("Authorization", "Basic "+basic(user, password))
	}

	return suite{seed: seed, strictSeed: true, scenarios: []Scenario{
		{Name: "server-info", Steps: []Step{
			serverInfo(""),
			basicAs(serverInfo(""), "admin@example.com", "secret"),
			basicAs(serverInfo(""), "user@example.com", "u"),
			// A public endpoint still fails on a malformed header (Java: runtime exception, 400).
			bearer(get("", "/api/server"), "not-a-token"),
			// An unsupported scheme just leaves the caller anonymous.
			get("", "/api/server").header("Authorization", "Digest abc"),
			get("", "/api/server").header("Origin", "https://sir.example"),
		}},

		{Name: "password-login", Steps: []Step{
			postForm("p", "/api/session", "email=admin%40example.com&password=wrong"),
			postForm("p", "/api/session", "email=nobody%40example.com&password=x"),
			postForm("p", "/api/session", "email=admin%40example.com"),     // null password: 400
			postForm("p", "/api/session", "password=secret"),               // null email: 400
			postJSON("p", "/api/session", `{"email":"admin@example.com"}`), // @Consumes(form): 415
			postForm("p", "/api/session", "email=disabled%40example.com&password=d"),
			postForm("p", "/api/session", "email=expired%40example.com&password=e"),
			postForm("p", "/api/session", "email=admin%40example.com&password=secret&code=abc"),
			postForm("p", "/api/session", "email=++ADMIN%40Example.COM+&password=secret"),
			get("p", "/api/session"),
			del("p", "/api/session"),
			get("p", "/api/session"),
			del("p", "/api/session"), // logged out: 401
			postForm("login", "/api/session", "email=loginname&password=l"),
			get("login", "/api/session"),
			get("anon", "/api/session"),
		}},

		{Name: "totp", Steps: []Step{
			postForm("t", "/api/session", "email=totp%40example.com&password=t"), // 401, WWW-Authenticate: TOTP
			postForm("t", "/api/session", "email=totp%40example.com&password=wrong&code=123456"),
			postForm("t", "/api/session", "email=totp%40example.com&password=t&code=${totp:"+totpSecret+"}"),
			get("t", "/api/session"),
		}},

		{Name: "basic-auth", Steps: []Step{
			basicAs(get("", "/api/session/${admin}"), "admin@example.com", "secret"),
			basicAs(get("", "/api/session"), "admin@example.com", "secret"), // reads only the session: 404
			basicAs(postForm("", "/api/session/token", "expiration=2030-01-01T00:00:00Z"), "admin@example.com", "wrong"),
			basicAs(postForm("", "/api/session/token", "expiration=2030-01-01T00:00:00Z"), "disabled@example.com", "d"),
			basicAs(postForm("", "/api/session/token", "expiration=2030-01-01T00:00:00Z"), "Admin@Example.com", "secret").token(),
			get("", "/api/session/${admin}").header("Authorization", "Basic "+b64("admin@example.com")), // no colon: 400
			get("", "/api/session/${admin}").header("Authorization", "Basic"),                           // no credentials: 400
			get("", "/api/session/${admin}").header("Authorization", "Digest abc"),                      // unsupported: 401
			del("", "/api/session").header("Accept", "text/html"),                                       // 401 + WWW-Authenticate
			del("", "/api/session"),
		}},

		{Name: "tokens", Steps: []Step{
			// Tokens signed by Java work on both sides.
			get("tok", "/api/session?token=${javaToken}"),
			get("tok", "/api/session"),
			bearer(get("", "/api/session/${admin}"), "${javaToken}"),
			bearer(get("", "/api/session/${admin}"), "${expiredToken}"),
			get("x1", "/api/session?token=${expiredToken}"),
			bearer(get("", "/api/session/${admin}"), "${revokedToken}"),
			get("x2", "/api/session?token=${revokedToken}"),
			bearer(get("", "/api/session/${admin}"), "not-a-token"), // malformed frame: 400
			bearer(get("", "/api/session/${admin}"), "AAAA"),        // empty signature: Java 500
			get("x3", "/api/session?token=AAAA"),                    // same token via ?token=: 401
			get("x4", "/api/session?token="),                        // empty: 400

			// Each side issues a token; the other side must accept it.
			login("admin", "admin@example.com", "secret"),
			postForm("admin", "/api/session/token", "expiration=2034-06-01T12:00:00Z").save("token", "$body").token(),
			get("cross", "/api/session?token=${token}").cross(),
			bearer(get("", "/api/session/${admin}"), "${token}").cross(),
			postForm("admin", "/api/session/token", "other=1").save("week", "$body").token(), // no expiration: 7 days
			bearer(get("", "/api/session/${user}"), "${week}"),

			// A session opened with a token can't mint a longer-lived one.
			postForm("tok", "/api/session/token", "expiration=2040-01-01T00:00:00Z").token(),
			postForm("tok", "/api/session/token", "expiration=2030-01-01T00:00:00Z").token(),
			postForm("tok", "/api/session/token", "other=1"), // no expiration: Date.before(null), 400
			postForm("tok", "/api/session/token", "expiration=tomorrow"),

			// Revocation.
			postForm("admin", "/api/session/token/revoke", "token=${token}"),
			bearer(get("", "/api/session/${admin}"), "${token}"),
			postForm("admin", "/api/session/token/revoke", "token=${token}"), // twice: 400
			postForm("admin", "/api/session/token/revoke", "token=not-a-token"),
			postForm("admin", "/api/session/token/revoke", "token=AAAA"),
			postJSON("admin", "/api/session/token/revoke", `{"token":"x"}`),
			bearer(get("", "/api/session/${admin}"), "${token}").cross(), // the other side's token is still valid
		}},

		{Name: "login-as", Steps: []Step{
			login("admin", "admin@example.com", "secret"),
			get("admin", "/api/session/${user}"),
			get("admin", "/api/session"), // the session now belongs to the user
			login("user", "user@example.com", "u"),
			get("user", "/api/session/${admin}"), // 400: SecurityException
			get("user", "/api/session/${user}"),  // yourself
			login("manager", "manager@example.com", "m"),
			get("manager", "/api/session/${managed}"),
			login("manager2", "manager@example.com", "m"),
			get("manager2", "/api/session/${user}"),
			login("admin2", "admin@example.com", "secret"),
			get("admin2", "/api/session/abc"),
			get("admin2", "/api/session/99999"),
		}},

		{Name: "openid-and-unknown", Steps: []Step{
			get("", "/api/session/openid/auth"),
			get("", "/api/session/openid/callback?code=x"),
			get("", "/api/nothing-here"),
		}},
	}}
}

// b64 encodes Basic credentials without a colon (malformed on purpose).
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func (s Step) cross() Step { s.Cross = true; return s }
