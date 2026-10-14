# API usage by in-house clients

Read-only survey (2026-10-14) of two client repositories next to this one. It complements the
access-log analysis in `README.md` section 7, which covers SIR-web, SIR-manager and SIR-client.
Credentials found in those repositories are not reproduced here.

## `../scripts/traccar-bots` (Bun/TypeScript simulators)

REST, all `GET` with **HTTP Basic auth** and `Accept: application/json`, through `getTraccarJson`
(`src/rj-simulator/traccar.ts`):

| Call | Must keep |
|---|---|
| `GET /api/devices` | `status` compared case-insensitively to `"online"`; `disabled`, `uniqueId`, `name`, `groupId`, `lastUpdate` (string or null) |
| `GET /api/positions` | latest position per visible device; `deviceId`, `fixTime`, `attributes`. A phone is recognized by the attribute keys `batteryLevel` or `os`. |
| `GET /api/groups?all=true` | returns groups owned by other accounts |
| `GET /api/geofences?groupId=N` | must filter (the client fails if the result isn't shorter than `?all=true`) |
| `GET /api/geofences?all=true` | `area` is WKT with `lat lon` order (POLYGON, MULTIPOLYGON, LINESTRING, CIRCLE); `attributes.polylineDistance` |

Retries happen on 429, 5xx and network errors. A 401 or other 4xx fails at once. Error bodies are
only displayed.

Device traffic: OsmAnd as `POST <endpoint>?id=…&lat=…&lon=…&timestamp=<ISO UTC Z>&speed=<knots>&bearing=…&altitude=…&accuracy=…&valid=true[&pushToken=…]`
with **no body**. Endpoints are `https://traccar…/position-mobile/` (prod and dev, a reverse-proxy
path) and `http://localhost:8082` in LOCAL mode. That LOCAL target is the web port, so it
probably doesn't work against a stock server.

Some scripts write to Postgres **directly**: `tc_geofences`, `tc_groups`, `tc_group_geofence`,
`tc_user_geofence` (`scripts/import-santo-andre-*.ts`; `cpd-trips.ts` and `balance-generator.ts`
weren't fully inspected). This is one more reason the schema and encodings must stay compatible.

## `../track-client-lib` (Expo module: JS, Kotlin, Swift)

No Traccar REST calls, no authentication, and no WebSocket. It only reports positions: OsmAnd as
`POST https://traccar…/position-mobile/` with `Content-Type: application/x-www-form-urlencoded`
and the **fields in the body**:

- `id`, `lat`, `lon`, `timestamp`, `speed` (knots, unrounded double), `heading` (not `bearing`),
  `accuracy`, `altitude`
- `batt` (e.g. `87.0`), `charge`, `is_moving`, `odometer=0`, `activity`, `os` (`android` / `ios`),
  optional `pushToken`

`timestamp` is ISO UTC `Z` on Android and iOS, but **local time with an offset** (`-03:00`) in
the JS task. A non-2xx reply queues the position for retry; the response body is not read.

## What the Go server must keep for these clients

- Basic auth on GETs; `?all=true` and `?groupId=` semantics; the device `status` strings.
- OsmAnd decoding on the `/position-mobile/` route: parameters in the query string *and* the form
  body, both `bearing` and `heading`, timestamps in UTC or with an offset, and `batt` stored as
  `batteryLevel` with `os` kept as an attribute.
- Check in the reverse proxy config which backend port `/position-mobile/` reaches (osmand 5055 or the web port).
