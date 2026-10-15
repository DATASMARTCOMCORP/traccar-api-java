# compat: Java-vs-Go differential harness

This is Step 1 of `../MIGRATION.md`. It runs the same scenarios against two servers and
reports every difference in HTTP responses and database rows. The Java jar is the
oracle.

```sh
# Prerequisites: Java 21, the jar (./gradlew assemble at the repo root), and Postgres
# binaries (initdb, pg_ctl, postgres), e.g. the zonky embedded-postgres-binaries jar.
cd server-go
go run ./compat -pg-bin /path/to/pgsql/bin              # self-check: Java vs Java
go run ./compat -pg-bin ... -b go -go-bin ./sir-server  # Java vs Go
go run ./compat -pg-bin ... -run 'bootstrap|tokens'     # some scenarios only
go run ./compat -pg-bin ... -keep -work /tmp/run        # keep DBs and logs, print DSNs
go run ./compat -pg-bin ... -b-tz UTC                   # canary: must FAIL (see below)
```

It exits 0 when every scenario matches, 1 on differences, and 2 on setup errors.

## Suites

| Suite | Starts from | Covers | Java vs Go |
|---|---|---|---|
| `full` (default) | an empty database | the whole API, device traffic, reports | passes once the API is ported (Steps 4–8) |
| `auth` | users, tokens and the key pair created by Java in a seed | Step 3: password, TOTP, Basic, bearer token and cookie logins; tokens across servers; revocation; login-as; Java's error statuses | **passes** (Step 3 gate) |

A suite's **seed** runs once, through Java, on the template database before both sides are
cloned. Both sides then start with the same Java-made rows (password hashes, the
`tc_keystore` key pair), and the seed's variables (`${javaToken}`, `${admin}`) are visible
everywhere. So a suite can test an endpoint Go has before Go has the endpoints that
create its data.

A step marked **cross** expands variables with the other side's values. A uses the token B
issued, and B uses A's: this is how the harness checks that each server accepts the
other's tokens. `${totp:SECRET}` expands to the current TOTP code for a base32 secret.

## What a run does

1. Starts a throwaway Postgres cluster in the work directory on a free port. It listens
   on `127.0.0.1` only, with Unix sockets disabled, so it can never touch another server.
2. Runs the **Java jar once** against an empty `schema_template` database, so Liquibase
   creates the schema from `schema/changelog-master.xml`. Go never runs migrations, which
   is why the schema always comes from Java.
3. Creates `side_A` and `side_B` as copies of that template, so both start byte-identical.
4. Starts both targets on free ports. Each target gets its own XML config (`<work>/A/config.xml`),
   in the same format and with the same keys production uses, so the Go side's config
   loader is exercised as well.
5. Runs the scenario library (`scenarios.go`) step by step, A then B, and compares each
   step's results. After device traffic it waits out `server.buffering.threshold`, then
   until both databases stop changing. After each scenario it compares every row of every
   table.

Both targets run with `TZ=America/Sao_Paulo` (`-tz`), and the JVM also gets `-Duser.timezone`.
The zone is non-UTC on purpose, so a server writing timestamps in the wrong zone shows up
as a 3-hour difference.

The config is hermetic: `geocoder.type=pluscodes` (offline, as in production) and an empty
`server.statistics`. Java's defaults would call an online geocoder and post statistics to
traccar.org. Position processing mirrors `sir-dev.xml` (`server.instantAcknowledgement`,
`server.buffering.threshold=5000`, `server.timeout=120`).

## How results are compared

Per step:
- **Status**: always compared.
- **Headers**: `Content-Type`, `Content-Disposition`, `Location`, `WWW-Authenticate`,
  `Access-Control-Allow-*`, and `Set-Cookie` (names and attributes; values are random).
- **Body**: skipped for error statuses (>= 400). The plan lets Go send a short message where
  Java sends a stack trace. JSON is compared semantically: key order and whitespace are
  ignored, but **numbers are compared by their literal text** (`87` vs `87.0`), and array
  order matters. Device replies (TCP/UDP ACKs) are compared byte for byte.

Database: every table in `public`, rows aligned by primary key (or compared as a
multiset without one), and every value compared as Postgres text (`col::text`).

### Allowed differences, and why

Each exception below is narrow. The unit tests in `compat_test.go` show that each one still
catches real differences.

| Where | Rule | Why |
|---|---|---|
| JSON strings in Java date format, `timestamp` columns, CSV time cells | equal within 5s | Each server stamps its own "now" (`serverTime`, `lastUpdate`, `actiontime`). A zone or format bug still shows as hours, or as a format mismatch. |
| `tc_users.hashedpassword`, `tc_users.salt` | both 48 hex chars | Random salt per user. Step 3 cross-verifies passwords. |
| `tc_keystore` keys | both non-empty | Each JVM generates its own key pair on first token. |
| Bearer token bodies | same user id and expiration in the signed payload (expiration within 5s when the server picks it) | The id and ECDSA signature are random. |
| Token ids (`tc_revoked_tokens.id`, `tc_actions.objectid`, `/api/audit`) | aliased | The harness decodes each issued token and maps A's random id to B's, so it still checks that the *right* token was revoked. |
| `/api/server` `storageSpace` (admin only) | same shape | The host's live free and total disk space. |
| `/api/notifications/notificators` | array order ignored | Java returns a `HashSet`; its order changes between JVM runs. |
| `geofenceIds` (any JSON) and `tc_positions.geofenceids` | same elements, any order | Java fills it from a `HashSet` of geofences whose order changes between JVM runs, so Java doesn't produce one stable text either. |
| `/api/positions/csv` | columns matched by name | Attribute columns come from a `HashSet`. |

The Java-vs-Java self-check (the Step 1 gate) passes only with these rules. Anything else
Java does differently between two runs would show up there first.

### Canary

`-b-tz UTC` runs side B's JVM in UTC. That run **must fail**, with every server-written
timestamp off by 3 hours. If it passes, the harness has stopped checking DB timestamps.

## Scenario library

Scenarios run in order and build on each other. `bootstrap` creates the admin user that
later scenarios use. Device payloads are copied from the Java protocol tests
(`src/test/java/org/traccar/protocol/*Test.java`), adjusted only where TCP framing needs it:
Atrack text frames get their real length field (`atrackText`), because unit tests bypass the
frame decoder.

| Scenario | Covers |
|---|---|
| bootstrap | first-user admin registration, closed registration, login/logout, server info |
| server-settings | `PUT /api/server`, open registration, self sign-up |
| users-and-roles | admin/manager/user/readonly/disabled/expired users, managed users, TOTP |
| tokens-and-auth | bearer tokens, `?token=` sessions, Basic auth, revocation |
| entities-crud | groups, devices, geofences, drivers, computed attributes, calendars, commands, notifications, maintenance, orders, reports |
| permissions | link tables (including linked devices), visibility per role, readonly denials |
| device-traffic | osmand (HTTP), suntech text and binary (with the SIR vectors), intellitrac, atrack text (TCP/UDP) and binary, positions API, accumulators |
| reports | route/events/summary/trips/stops/combined/geofences JSON, xlsx, audit, statistics |
| commands | command types, sending, queueing for offline devices |
| deletes | entity deletes, position deletes, logout |

The oracle's current behavior is what gets compared, including behavior that looks wrong.
The SIR two's-complement Suntech vectors are rejected by the current jar, and one
geofence exceeds `VARCHAR(4096)` because `changelog-6.13-dsc` no longer runs. See
`../MIGRATION.md`, "Findings during Steps 1 and 2".
