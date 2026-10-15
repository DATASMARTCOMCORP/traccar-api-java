# SIR server: Java → Go migration plan

Status: **approved**. Steps 0 (inventory, `inventory/results/`), 1 (harness), 2 (foundation),
3 (auth and sessions), 4 (read-only API), 5 (write API) and 6 (ingestion core, OsmAnd) are done. Decisions and findings are in section 8.

## 1. Goals and hard constraints

1. **Web API 100% compatible.** Same paths, methods, query/form/JSON parameters,
   status codes, JSON field names, JSON value formats, cookies and auth schemes.
   SIR-web, SIR-manager and SIR-client must not need any change.
   - Exception (agreed): error bodies no longer contain Java stack traces.
     Status codes stay identical, and the body is a short plain-text message.
2. **Database 100% compatible.** Same schema (the current Liquibase changelog, including
   `changelog-6.13-dsc`), same column encodings, same row semantics. Java and Go
   must be able to run against the same database at the same time during the transition.
3. **Same XML config file** (`traccar.xml` / `sir-dev.xml` shape, same keys,
   same defaults, same `CONFIG_USE_ENVIRONMENT_VARIABLES` override).
4. **Idiomatic Go, not transliterated Java.** See section 3.

## 2. Where we start (facts gathered)

- Codebase: Traccar **6.14.5** (synced with upstream on `master`, commit `907139a13`),
  1,045 Java files, about 116k LOC: about 77k in `protocol/` (200+ protocols),
  about 39k in the server core. 426 test files, mostly protocol tests with real
  device payloads. These become our Go test vectors.
- SIR-specific changes on top of upstream (everything else is stock Traccar):
  - `SuntechProtocolDecoder`: two's-complement coordinate decoding with a
    signed-magnitude fallback, plus tests.
  - `schema/changelog-6.13-dsc.xml`: `tc_geofences.area` widened to `TEXT`.
  - Suntech ports 47762/47763. These are now set in `sir-dev.xml`. The upstream
    sync reset the code default back to 5011.
  - `traccar-web` submodule points to the SIR fork.
- Every protocol has a default port, so an unrestricted config listens on about 250 ports.
- Features enabled in `sir-dev.xml`:
  - ports: osmand 5055, suntech 47762 / suntech_udp 47763, intellitrac 6037, plus `atrack.custom`
  - `geocoder.type=pluscodes`
  - `notificator.types=web,mail,sms,command,firebase` (SMS goes through `sms.http.url`)
  - `event.forward.type=json` (webhook)
  - `server.instantAcknowledgement`, `server.buffering.threshold`
- Other assets to port:
  - 31 Liquibase changelogs
  - 28 notification templates × 10 languages (Velocity `.vm`)
  - 6 jxls Excel templates
  - 5 `.proto` files
  - `openapi.yaml`

### Compatibility details verified against the running 6.14.5 Java server

| Area | Java behavior (must be reproduced) |
|---|---|
| Timestamps in DB | `timestamp without time zone`, written as **wall-clock time in the JVM default zone** (16:40Z was stored as `13:40` on a UTC-3 host). Go needs a `database.timezone` setting defaulting to the host zone, and we must check which zone the production JVM uses. |
| Dates in JSON | `2026-10-14T19:11:35.225+00:00` (UTC, millis, `+00:00`); `null` when unset. Input parsing is lenient (Jackson `StdDateFormat`). |
| `attributes` / `network` / `geofenceIds` columns | JSON text. Jackson keeps the int vs. double distinction (`87.0` vs `87`), and Go must preserve it on round-trip. |
| Foreign-key columns (`*Id`) | `0` is written as `NULL`; `NULL` is read back as `0`. |
| Column names | Unquoted camelCase, so Postgres folds them to lowercase. Some ID columns are `bigint` since 6.14 (`tc_devices.positionid`, `tc_actions.userid`, …). |
| Passwords | PBKDF2-HmacSHA1, 1000 iterations, 24-byte salt and hash, hex-encoded (`tc_users.hashedpassword`, `salt`). |
| Bearer tokens | ECDSA P-256 SHA-256 (DER signature) over JSON `{"i":tokenId,"u":userId,"e":date}`, framed as `[sigLen][sig][payload]`, base64url. Keys are DER (X.509 / PKCS#8) in `tc_keystore`, and Go must reuse the same keys. Revocation goes through `tc_revoked_tokens`. |
| Web sessions | `JSESSIONID` cookie with server-side session state. Sessions are not shared between Java and Go (users re-login once at cutover; tokens keep working). |
| Permissions | `tc_<owner>_<property>` link tables (now including `tc_device_device` for linked devices), with recursive group expansion (a UNION query over group parents). |
| Latest positions | `GET /api/positions` without `deviceId` only returns fixes newer than `database.positionPeriod` (default 90 days). |
| Device state | `status`, `lastUpdate`, `positionId` updated separately from normal device edits. Motion state includes `motionLatitude`/`motionLongitude`. |
| Position processing | Asynchronous after the device ACK; fixed handler order (Outdated → Time → Geolocation → Hemisphere → Distance → Filter → Geofence → Geocoder → SpeedLimit → Motion → … → Database → event handlers → PostProcess). |

## 3. Go design rules (removing the Java-isms)

- **No DI container.** `main.go` builds everything explicitly. Constructors take what they need.
- **No inheritance chains.** `BaseProtocolDecoder → ExtendedObjectDecoder → Netty`
  becomes a small `Decoder` interface plus a `*Session` value that carries the shared helpers.
- **No getters or setters.** Use exported fields. Per-protocol options are read once into an `Options` struct.
- **No reflective generic storage.** Use explicit SQL per entity (pgx), with small
  generic helpers only where the code is truly identical (link tables).
- **Errors are values.** Wrap with `%w` and pass `context.Context` everywhere.
- **Netty becomes the standard library.** `net.Listener` and `net.PacketConn`, one goroutine per connection,
  frame decoders as `bufio.SplitFunc`.
- **Handler chain becomes a slice.** An ordered `[]func(ctx, *Position) error`.
- **Jersey becomes `net/http`.** `http.ServeMux` with Go 1.22+ patterns and a few middlewares.
- **Caches become plain maps.** `CacheManager`/`CacheGraph` turn into maps behind `sync.RWMutex`.
- **No `helper`/`util` dumping ground.** Each helper lives next to its users.
- **Logging.** Use `log/slog`.

Planned layout:

```
server-go/
  cmd/sir-server/          main: load config, wire, run, shut down
  internal/config/         XML properties loader, typed keys, per-protocol ports
  internal/model/          plain structs + JSON/DB compatibility types (Time, Attributes)
  internal/store/          Postgres access, permissions SQL, keystore
  internal/auth/           password hashing, tokens, sessions, TOTP, OpenID
  internal/api/            REST + WebSocket handlers, middleware
  internal/ingest/         listeners, device sessions, command queue
  internal/protocol/<name> one package per protocol
  internal/pipeline/       position processing stages
  internal/event/          event detectors
  internal/notify/         notificators + templates
  internal/report/         reports + Excel export
  internal/forward/        position/event forwarders
  internal/geo/            geofences, geocoders, geolocation
  compat/                  Java-vs-Go differential test harness
```

## 4. Repository strategy

Use the `go-migration` branch of this repo (`traccar-api-java`, branched from `master`),
with the Go module in `server-go/`. The Java code stays buildable on the same branch,
because the Go side reuses its Liquibase changelogs, `openapi.yaml`, templates and test
payloads, and the compat harness runs both servers side by side. After Java is
retired, either move `server-go/` to the repo root or split it into a new repo
with `git subtree split` (history preserved).

## 5. Steps

Each step ends with a **gate**: its exit criteria must pass before the next step starts.

### Step 0: Inventory (no Go code)
- Run against production (read-only):
  - protocols in use over the last 90 days (`tc_positions.protocol`), with device counts per protocol
  - which notificators, forwarders, geocoder, reports and computed attributes (`tc_attributes`) are in use
- Pull API access logs to list the endpoints SIR-web, SIR-manager and SIR-client actually call.
- Confirm the production JVM time zone (`user.timezone` / host TZ).
- Rotate the credentials committed in `sir-dev.xml` (database, SMTP, webhook
  bearer token) and move them out of git.

**Gate:** frozen lists of protocols, endpoints and features to port.

### Step 1: Compatibility harness
- `compat/`: starts the Java jar and the Go binary against throwaway Postgres
  instances created from the same Liquibase schema, sends identical
  requests and device traffic, and diffs:
  - HTTP status, headers that matter, and JSON bodies (compared semantically)
  - DB rows after each scenario (all tables, normalized)
- Scenario library: login/session/token, CRUD per entity, permissions
  (user/manager/admin/readonly), reports, and device traffic replays.

**Gate:** harness runs green against Java vs. Java (a self-check).

### Step 2: Foundation
- Config loader (XML properties, env override, default protocol ports).
- Model structs with compatibility types (`model.Time`, `model.Attributes` keeping int/float).
- Store: connection pool, time-zone handling, schema version check against
  `databasechangelog` (Go refuses to start on an unknown schema and never runs Liquibase).
- `main.go`, graceful shutdown, slog logging, CI building a static binary.

**Gate:** unit tests pass, and DB round-trip tests show identical stored text for rows written by Java.

### Step 3: Auth and sessions
- PBKDF2 password check and creation, bearer tokens with revocation (verifying
  Java-issued tokens and the reverse), Basic auth, `JSESSIONID` cookie sessions,
  service-account token, TOTP, OpenID (if used per Step 0), LDAP (if used).

**Gate:** the harness passes all session scenarios, and tokens interoperate in both directions.

### Step 4: Read-only API
- `server`, `session`, `users`, `devices`, `groups`, `positions`, `events`,
  `geofences`, `drivers`, `attributes`, `calendars`, `commands`, `notifications`,
  `maintenance`, `orders`, `permissions` (GET), `statistics`, `health`.
- Full permission semantics (`all`, `userId`, `groupId`, `deviceId`, `excludeAttributes`).

**Gate:** every GET endpoint in use matches Java in the harness for all roles.

### Step 5: Write API
- POST/PUT/DELETE for all entities, permission links (including linked devices),
  device share, accumulators, device image upload, password reset, token revocation,
  audit log (`tc_actions`).

**Gate:** the harness shows identical responses and identical DB rows.

### Step 6: Ingestion core
- TCP/UDP/HTTP listeners, device session lookup, `database.registerUnknown`,
  online/offline/unknown status with timeouts, queued commands,
  `server.instantAcknowledgement`, `server.buffering.threshold`.
- Position pipeline stages in Java order (Outdated, Time, Hemisphere, Distance, Filter, Motion,
  CopyAttributes, EngineHours, Database, PostProcess…).
- First protocol: **osmand** (HTTP), which proves the pipeline end to end.

**Gate:** replayed osmand traffic produces identical `tc_positions` / `tc_devices` / `tc_events` rows.

### Step 7: Protocols (all of them; repeat per protocol)
- Port the decoder and encoder (commands), and its Java test vectors as Go table tests.
- Replay captured production traffic through Java and Go, then diff the decoded positions.
- Order: **osmand** (Step 6, special focus: every Java test vector, plus replay of captured
  production traffic covering both client styles in `inventory/clients.md`), then **suntech**
  (including the SIR fix), intellitrac and atrack (custom mode). After that, **every other
  protocol**, batched by shared frame decoders and helpers. Each protocol is ported with its Java
  test vectors as Go table tests and checked in the harness. Without production traffic, the
  Java test vectors are the oracle.

**Gate per protocol:** all Java test vectors pass, and the traffic replay diff is empty.

### Step 8: Live updates and events
- WebSocket `/api/socket` (devices/positions/events/logs push), event detectors
  (overspeed, motion, geofence, ignition, alarm, fuel, maintenance, driver, behavior, media, command result).

**Gate:** identical event rows and WebSocket messages in the harness.

### Step 9: Notifications, reports, forwarding, scheduled jobs
- Notificators: web, mail, sms (HTTP), command, firebase. Templates converted
  from Velocity `.vm` to `text/template` for all 10 languages.
- Reports: route, events, trips, stops, summary, combined, scheduled reports. Excel export with `excelize`.
- Forwarders: `event.forward` JSON webhook, plus any others found in Step 0.
- Geocoder: pluscodes.
- Computed attributes: JEXL becomes `expr-lang/expr`, with a translation and audit of stored expressions.
- Scheduled tasks: statistics, device inactivity, expirations, retention cleanup.

**Gate:** harness parity for reports (JSON and xlsx contents), and rendered notification text matches.

### Step 10: Shadow run and cutover
- Run Go in staging against a production DB copy, replaying mirrored traffic.
- Production cutover, port by port: move protocol ports from Java to Go (both write
  to the same DB). Commands for a device must reach the process that holds its
  connection. Either keep the API on Java until all ports have moved, or
  implement Traccar's Redis broadcast in Go.
- Switch the API last. Users with cookie sessions log in again once; tokens keep working.
- Rollback at any point means pointing the ports or API back at Java, because the DB stays compatible.

**Gate:** a stable week with no diffs, then Java is retired.

### Step 11: After the switch
- Freeze Liquibase. New schema changes go through Go-side migrations (goose), baselined at the final changelog.
- Remove Java from the branch, then merge to `master` (or split the repo).

## 6. Risks

1. **Losing upstream Traccar.** This repo tracks upstream closely (last sync: 6.14.5).
   After the switch, protocol fixes no longer arrive for free and must be ported by hand.
2. **JSON edge cases** (number formatting, nulls, date input formats). Mitigated by the harness.
3. **JEXL → expr syntax differences** in expressions already stored in the DB.
4. **Decoder subtleties** (`ByteBuf` indexes, per-connection state). Mitigated by test vectors and traffic replay.
5. **Time zone of stored timestamps.** Must match the production JVM exactly.

## 7. Open decisions

- ~~Port only protocols in use (recommended) or all 200+?~~ All of them, with OsmAnd first (section 8).
- Postgres only (recommended; production uses Postgres), or keep H2/MySQL/MSSQL?
- Keep the API on Java until all ports have moved (simpler), or build the Redis broadcast early?

## 8. Decisions and findings during Steps 1 and 2

Decisions (from the review of section 7): **Postgres only**. **Port every protocol** (all 267
classes, 266 with default ports; revised on 2026-10-15, superseding "only the protocols in use").
**OsmAnd gets special focus:** it is the only protocol production uses today, so it goes first,
with the deepest testing and traffic replay. Then suntech (with the SIR two's-complement fix),
intellitrac and atrack, then the rest.
**Keep the API on Java until every device port has moved**; no Redis broadcast for now.

**The Go Suntech decoder keeps the SIR fix** (decided after finding 1 below): binary coordinates
are read as two's complement first, with a fallback to signed magnitude, as on `907139a13^1`. For
those vectors the reference is the pre-merge decoder, not the current jar. In Step 7 the harness
replays them against a jar built from `907139a13^1`, or lists them as an expected, documented
divergence from the current oracle. Either way, the fix is checked, not skipped.

Findings while building the harness. These were checked against the jar built from `go-migration`
and need a decision before the steps that depend on them:

1. **The SIR Suntech fix was lost in the upstream merge `907139a13`.** Its first parent decoded
   binary coordinates as two's complement with a signed-magnitude fallback. The merged
   `SuntechProtocolDecoder` is stock upstream (signed magnitude only). The SIR tests
   `testDecodeTwosComplement` and `testDecodeBrazilianCoordinates` now **fail** ("Latitude out of
   range"), and the server drops those messages and closes the connection. Any production jar built
   after the merge loses binary Suntech fixes with negative coordinates (all of Brazil). For Step 7,
   the reference behavior for those vectors is the pre-merge decoder, not the current oracle.
   `inventory/README.md` §4b has the production check.
2. **`changelog-6.13-dsc.xml` is no longer included** by `schema/changelog-master.xml` (also lost in
   `907139a13`). A database created or upgraded by the current jar keeps `tc_geofences.area` as
   `VARCHAR(4096)`, and geofences above 4096 characters fail with 400 (the harness has such a step).
   The Go schema check therefore accepts the DSC changeset as optional. Production (§1 of the
   inventory) decides whether it's required.
3. **`suntech_udp.port` (in `sir-dev.xml`) has no effect.** No protocol named `suntech_udp` exists,
   and Java opens no UDP Suntech port.
4. **Java defaults that reach the network:** `geocoder.type` defaults to the online `locationiq`
   geocoder (production overrides it with pluscodes), and `server.statistics` defaults to posting
   daily counts to traccar.org unless set to an empty value. `sir-dev.xml` doesn't set it.
5. **`server.buffering.threshold` defaults to 3000 ms** (production: 5000). Fixes are held in memory
   that long before processing.
6. **Java is not deterministic everywhere.** These outputs vary between two runs of the same jar:
   `geofenceIds` order (API and `tc_positions.geofenceids` text, built from a HashSet),
   `/api/notifications/notificators` order, and `/api/positions/csv` column order. "Byte-identical"
   can't apply to them, so the harness compares them as sets (see `compat/README.md`). Network
   `cellTowers` and `tc_statistics.protocols` also come from hash-ordered Java collections. Go must
   emulate that order when it creates new rows (Steps 7 and 9).
7. **Time zones:** when the JVM reads a wall-clock time that is ambiguous or doesn't exist (DST
   transitions; São Paulo had DST until 2019), it resolves it to the **standard** offset. Go
   reproduces this explicitly (`internal/store`, tested with vectors from the oracle).

### Step 0 results (2026-10-15, `inventory/results/2026-10-15.md`)

- **Finding 1 confirmed:** production runs the jar from the current `master` build (same
  SHA-256), so it lacks the SIR Suntech fix. Today it loses nothing, because production receives
  **only OsmAnd** traffic: all 370 active devices, and every protocol counted in the last 30 days.
- **Finding 2:** production *has* `changelog-6.13-dsc` (`area` is TEXT and geofences reach
  270k characters), but lacks `6.13.0-fk-linkeddeviceid` and `6.15.0`, and carries 25 legacy
  3.x/4.0 rows. The Go schema check accepts exactly that history. The harness oracle still
  builds its schema from the current changelog (no DSC, with 6.15.0), so its geofence and id
  column types differ from production's. Make it build a production-shaped schema before the API
  steps compare geofences.
- **Time zone:** America/Sao_Paulo on the host, the JVM and the DB. JDK 21 confirmed.
- **Production config differs from `sir-dev.xml`:** `server.buffering.threshold=0`, and
  `time.zone=America/Sao_Paulo`.
- **Scope consequences:** all protocols are ported anyway (decision above). OsmAnd is the only
  protocol carrying production traffic, so it gets the deepest testing. No computed attributes, scheduled reports or saved commands. Auth: passwords,
  sessions, Basic auth and tokens only (no TOTP/LDAP/OpenID). The endpoint list is still open:
  production has no request log.

### Step 3 (auth and sessions), 2026-10-15

Gate passed: the harness `auth` suite (80 steps) matches **Java vs Go** in statuses, headers,
JSON and database rows. That includes tokens across servers in both directions (A uses B's
token and the reverse) and revocation. Separately, Java's own `CryptoManager` and
`TokenManager` loaded a Go-generated key pair and verified a Go-issued token (a manual check,
recorded here). Go reads Java's password hashes and token vectors in unit tests.

Scope, per Step 0: password (PBKDF2), TOTP, Basic, bearer tokens (`tc_keystore`,
`tc_revoked_tokens`), cookie sessions (`JSESSIONID`, in memory, as Jetty), the service account
token, and login-as. Not ported, because production doesn't use them: OpenID (it answers
400, "not enabled", like Java without it) and LDAP.

Not done yet, noted for later steps:
- `web.persistSession` (Jetty JDBC sessions)
- per-request statistics (`tc_statistics.requests`)
- OPTIONS preflight responses
- a 405 for a wrong method on a known path: everything under `/api/` that doesn't match a
  route currently gets 404

### Step 4 (read-only API), 2026-10-15

Gate passed: the harness `read` suite (8 scenarios, 230 steps) matches **Java vs Go** for the
admin, manager, user, readonly and anonymous roles, in statuses, headers, JSON and database
rows. Its seed is the whole `full` library run through Java, plus a 4-level group chain.

Permissions are a port of Java's SQL, statement for statement, including its limit: groups are
followed to the grandparent only (the harness checks that a great-grandchild device is denied
by both servers). Several values are generated from the Java sources and kept in sync by tests:
- the command and event type lists
- each protocol's supported commands, returned in Java's `HashSet` order, which Go reproduces
  (checked against JDK output)
- the default ports
- the time-zone list, which is JDK 21.0.12's, as in production

Java quirks reproduced:
- devices reset to offline at startup
- `/api/permissions` keys are lower case, because Postgres folds the column labels
- a missing object is a 404 with Jetty's HTML error page Content-Type
- a query parameter that doesn't convert is a 404, not a 400

Not done in Step 4:
- position exports (CSV, GPX, KML): Step 9, with reports
- ~~`GET /api/positions?geofenceId=`~~: done in Step 6, with the geofence geometry
- `/api/server/geocode`: Step 9, with geocoders
- `/api/server/cache`: a dump of Java objects, which Go won't reproduce
- the FindHub command sender

### Step 5 (write API), 2026-10-15

Gate passed. Java vs Go match in responses and database rows, row ids included:
- **`crud`** (8 scenarios, 143 steps): the library from an empty database.
- **`write`** (6 scenarios, 98 steps): everything else in Step 5.

Request bodies are read the way Jackson reads them, from an explicit property table per Java
class:
- unknown properties fail, except on `Server` and `Command`, and `@JsonIgnore`d properties are
  dropped;
- Jackson's scalar coercions apply ("7" to 7, 3.9 to 3, 1 to true);
- setters keep their side effects (trimming, password hashing, the iCal check);
- an explicit null fails in a dereferencing setter;
- an absent `NOT NULL` property fails at insert time.

Java behaviors this step had to reproduce:
- A create that fails on a `NOT NULL` column still uses an id sequence value, so later ids
  shift. Go consumes one too.
- `PUT /api/<entity>/abc` succeeds: Java's update never reads the path id, and updates the id
  in the body.
- Body mapping errors are 400 with `text/plain`. Empty 404s carry Jetty's HTML error page
  Content-Type, but only for GET and POST.
- Updating a missing row is not an error, and link tables accept duplicate rows.
- A command sent to a group queues the devices in a different order on every Java run (a
  HashSet of identity-hashed devices). Go uses id order; the harness tests a one-device group.

Not done in Step 5, because it needs later steps:
- live command delivery to connected devices (Step 6);
- SMS commands, push command senders, password-reset e-mail, notification tests and
  computed-attribute tests (Step 9).

The SMS and push command paths answer 501 in Go until then. The server reboot endpoint is not
ported.

### Step 6 (ingestion core, OsmAnd), 2026-10-15

Gate passed: replayed OsmAnd traffic gives identical rows in every table, `tc_positions`,
`tc_devices` and `tc_events` included, Java vs Go. Java vs Java passes the same suites.
- **`ingest`** (10 scenarios, production's config: no buffering). It covers:
  - every input form of the decoder: query string, form body, Transistorsoft JSON, each
    timestamp format, cells and Wi-Fi, Java number syntax, escapes;
  - its error paths: 400 and 404 replies, and connections closed without a reply;
  - every vector of `OsmAndProtocolDecoderTest`, and the requests of the in-house clients
    (`inventory/clients.md`);
  - queued commands returned in replies;
  - a drive that triggers every event detector;
  - the position filter, unparseable geofences, proximity, engine hours, copied
    attributes, `time.override`, positions without a fix, and calendars on devices and
    geofences.
- **`buffered`**: `server.buffering.threshold` (5 s) on one connection.
- **`status`**: `event.status.enable`, `status.timeout` and `database.registerUnknown`.

Go now receives OsmAnd on its port and processes positions like Java:
- device sessions, unknown devices (with registration and throttling), disabled devices;
- online and unknown status with the status timeout;
- queued commands, buffering, and delayed acknowledgement;
- the position handlers in Java's order: outdated, time, hemisphere, distance, filter
  (calendars included), geofences, motion, linked driver, copied attributes, engine hours,
  database, post-processing;
- every event detector, with the events stored in detection order.

Device rows, groups, the server row, geofences and calendars are read from the database for
each position. Java reads them from its cache, which its own API keeps current, so the
result is the same while the API stays in another process. Position history (the last
position, and the window the motion logic uses) follows the lifetimes of Java's cache
exactly: loaded with a device's first reference (a connection or a position in flight),
dropped with the last.

Exactness needed ports of Java behavior below the Traccar code. Each is checked against
output from the production JVM (Corretto 21.0.12):

| What | Go package | Checked against |
|---|---|---|
| `Math.sin` and `Math.cos`: HotSpot intrinsics, translated from the x86_64 stub sources and run on an emulated machine | `internal/javamath` | 3M JVM results, 2M `DistanceCalculator` results |
| `Math.atan2` (fdlibm) | `internal/javamath` | same |
| `Double.parseDouble`, `Integer.parseInt`, `Long.parseLong` | `internal/model` | 30k inputs |
| Geofence geometry (`GeofenceCircle`, `Polygon`, `Polyline`) and `containsPosition` | `internal/geo` | 300k checks |
| `DateUtil.parseDate` (`ISO_ZONED_DATE_TIME`) and `yyyy-MM-dd HH:mm:ss` | `internal/javatime` | 200k inputs |
| Netty's `QueryStringDecoder`, with `java.lang.String`'s UTF-8 replacement | `internal/protocol/osmand` | 50k inputs |
| `Calendar.checkMoment`: ical4j 4.2.5 parsing and recurrence | `internal/ical` | 1.7M checks of random calendars |

Findings:
1. **Buffering is racy in Java across connections.** Released fixes run on the thread of the
   connection that delivered them. If one device's fixes arrive over several connections,
   their processing order is a race, and two Java runs differ. Production sets the threshold
   to 0, so this doesn't happen there. Go releases in deadline order. The `buffered` suite
   checks one connection, where Java is deterministic.
2. **ical4j uses its own zone rules.** It builds them from VTIMEZONE files it bundles, and the
   result for America/Sao_Paulo still switches to -02:00 every January (back on the third
   Sunday of February). Calendar arithmetic in production follows those rules, so hourly
   rules and times near those fake transitions are off by an hour in January. Go embeds
   ical4j's effective rules for every zone (`internal/ical/ical4j-zones.txt.gz`).
3. **Calendar quirks Go reproduces:**
   - an occurrence of a recurring event includes both ends;
   - a single event excludes its start, so a moment exactly at DTSTART is outside;
   - all-day recurring events also cover the next day (by UTC date);
   - an error in any VEVENT fails the whole check.
4. **Dead keys in `sir-prd.xml`.** Java ignores both, and so does Go:
   - `server.instantAcknowledgement`: renamed `server.delayAcknowledgement`, with the
     meaning inverted (upstream c2b1ed594, 2025-07-06).
   - `time.zone`: 6.14.5 has no such key. Decoders read `decoder.timezone`, default UTC.
     OsmAnd doesn't use either; this matters for Step 7.
5. **Unparseable geofences switch off geofencing for their devices.** A geofence Java can't
   parse fails the whole geofence handler for every device linked to it. Production has such
   geofences (MULTILINESTRING, decimal commas). Those devices never get `geofenceIds` or
   geofence events, so their notifications never fire. Go reproduces this.
   `inventory/README.md` has a query that lists the affected devices.
6. A disabled or expired device makes the decoder throw. Java closes the connection without
   a reply, and so does Go.
7. With `event.status.enable`, Java compares the new status with the cached device's. When
   the device isn't cached, it compares with the blank-padded `CHAR(8)` column, which never
   matches, so an event fires. Go keeps the same state. Production doesn't enable status
   events.

Coexistence (for Step 10):
- **Status reset at startup.** Go resets every device to offline at startup, as Java does.
  With both running, restarting either one marks the other's devices offline until their
  next message.
- **Statistics.** Go writes its own daily `tc_statistics` row (messages received and stored,
  devices per protocol), so there will be two rows a day. Posting to `server.statistics` is
  not ported.

Not in Step 6:
- **Notifications and event forwarding (Step 9).** Go stores events but doesn't notify or
  forward them. Production uses both: web and mail on geofence events, SMS on motion, and
  the `event.forward` webhook. **The OsmAnd port must not move to Go before Step 9.**
- Computed attributes, geocoding of positions, geolocation, map matching, speed limits and
  position forwarding. Go logs a warning at startup when they are configured. Production
  uses none of them.
- The other protocols and live commands on TCP connections (Step 7), and WebSocket updates
  (Step 8).

**Licensing.** `internal/javamath/libm_gen.go` is translated from HotSpot's x86_64 libm stubs:
Intel's code, under GPLv2 only. It carries their notice. Bit-exact `Math.sin` and `Math.cos`
need this code. Before the Go server is distributed outside the company, decide whether that
license fits.

