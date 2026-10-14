# SIR server: Java → Go migration plan

Status: **draft, awaiting approval**. No Go code is written until this plan is approved.

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

### Step 7: Protocols (repeat per protocol from the Step 0 list)
- Port the decoder and encoder (commands), and its Java test vectors as Go table tests.
- Replay captured production traffic through Java and Go, then diff the decoded positions.
- Expected order: **suntech** (including the SIR two's-complement fix), intellitrac,
  atrack (custom mode), then the rest by device count.

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

- Port only protocols in use (recommended) or all 200+?
- Postgres only (recommended; production uses Postgres), or keep H2/MySQL/MSSQL?
- Keep the API on Java until all ports have moved (simpler), or build the Redis broadcast early?

## 8. Decisions and findings during Steps 1 and 2

Decisions (from the review of section 7): **Postgres only**. **Port only the protocols production
uses**, starting with osmand, suntech (with the SIR two's-complement fix), intellitrac and atrack.
**Keep the API on Java until every device port has moved**; no Redis broadcast for now.

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
