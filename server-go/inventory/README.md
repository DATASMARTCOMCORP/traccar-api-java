# Step 0 inventory: queries and checks to run in production

Step 0 of `../MIGRATION.md` needs production access, so this file lists what to run
and what each result decides. Everything here is **read-only**.

- SQL runs in a `READ ONLY` transaction with a statement timeout, so a mistake can't
  write or lock anything. Run it on a replica if one exists.
- Queries marked **(heavy)** scan `tc_positions` or `tc_events`. Run them off-peak, or skip
  them if the cheap query next to them already answers the question.
- Paste the outputs (redacting e-mails, phones and tokens) into `results/` next to this
  file, one file per section, so the Step 0 gate can be reviewed.

Open the session like this (psql):

```sql
BEGIN TRANSACTION READ ONLY;
SET LOCAL statement_timeout = '120s';
-- ... queries below ...
ROLLBACK;
```

Timestamps are stored as `timestamp without time zone` holding the **JVM's local wall-clock
time** (see section 2). The queries therefore compare against `localtimestamp`, which is
only correct when the psql session `TimeZone` equals the JVM zone. Run `SET LOCAL TimeZone =
'<JVM zone from section 2>';` first.

## 1. Schema version (decides the Go schema check)

Go refuses to start unless `databasechangelog` matches a known set of changesets, so this must
be known exactly.

```sql
SELECT id, author, filename, dateexecuted, orderexecuted, exectype, md5sum, liquibase
FROM databasechangelog ORDER BY orderexecuted;

SELECT * FROM databasechangeloglock;

SELECT table_name, column_name, data_type, character_maximum_length, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = 'public'
ORDER BY table_name, ordinal_position;

SELECT extname, extversion FROM pg_extension;
SELECT version();
```

What to look for:
- Is `changelog-6.13-dsc` (author `dsc-trackhub`) present? The upstream merge `907139a13`
  removed its `<include>` from `schema/changelog-master.xml`. A database created or
  upgraded by the current jar no longer gets `tc_geofences.area` widened to `TEXT`. See
  `../MIGRATION.md`, "Findings during Steps 1 and 2".
- Is `changelog-6.15.0` present? If it isn't, production runs an older jar than this branch,
  and the Go schema check needs that older changeset set.
- `tc_geofences.area`: `text` or `character varying(4096)`?
- Is `timescaledb` installed? (`changelog-6.8.0-timescale`/`6.11.0-timescale` are `MARK_RAN` without it.)

## 2. Time zone (decides `database.timezone` for Go)

On the database:

```sql
SHOW TimeZone;
SHOW DateStyle;
SELECT now(), localtimestamp;
-- Compare the newest server-written timestamp with "now" in a few candidate zones.
-- The zone where the difference is seconds (not hours) is the JVM zone.
SELECT max(servertime) AS newest_servertime,
       now() AT TIME ZONE 'UTC'               AS utc_now,
       now() AT TIME ZONE 'America/Sao_Paulo' AS sao_paulo_now
FROM tc_positions WHERE id > (SELECT max(id) - 1000 FROM tc_positions);
```

On the Java host (shell):

```sh
timedatectl                                   # host zone
systemctl show traccar -p Environment         # TZ= or JAVA_OPTS overrides (service name may differ)
ps -o args= -C java                           # look for -Duser.timezone=
# The JVM's effective default zone, using the same java binary and environment as the service:
java -XshowSettings:properties -version 2>&1 | grep -E 'user.timezone|java.version'
```

## 3. Which build runs in production

```sh
unzip -p /opt/traccar/tracker-server.jar META-INF/MANIFEST.MF   # Implementation-Version
ls -l --time-style=full-iso /opt/traccar/tracker-server.jar
ss -ltnup | grep java                                           # ports actually bound
```

Also record the git commit the deployed jar was built from. If it was built after
`907139a13`, the SIR Suntech two's-complement fix is **not** in it (see 4b).

## 4. Protocols in use (decides the list of protocols to port)

Cheap: the latest fix of every device that reported in the last 90 days.

```sql
SELECT p.protocol, count(*) AS devices
FROM tc_devices d JOIN tc_positions p ON p.id = d.positionid
WHERE d.lastupdate > localtimestamp - interval '90 days'
GROUP BY p.protocol ORDER BY devices DESC;
```

Cheap: daily message counts per protocol. Traccar records these in `tc_statistics.protocols`
when `server.statistics` is set, but the table is filled either way.

```sql
SELECT capturetime, activedevices, messagesreceived, messagesstored, protocols, requests
FROM tc_statistics ORDER BY capturetime DESC LIMIT 30;
```

**(heavy)** Exact device counts per protocol over 90 days:

```sql
SELECT protocol, count(DISTINCT deviceid) AS devices, count(*) AS positions,
       min(fixtime) AS first_fix, max(fixtime) AS last_fix
FROM tc_positions
WHERE fixtime > localtimestamp - interval '90 days'
GROUP BY protocol ORDER BY devices DESC;
```

### 4b. Suntech binary messages and the two's-complement fix

The current jar drops binary Suntech messages that carry two's-complement negative
coordinates, which covers all of Brazil. Two SIR tests fail with "Latitude out of range". A
device in that state stays `online` (its connection is alive) while its last fix gets old:

```sql
SELECT d.id, d.uniqueid, d.status, d.lastupdate, p.fixtime, p.servertime,
       d.lastupdate - p.servertime AS gap
FROM tc_devices d LEFT JOIN tc_positions p ON p.id = d.positionid
WHERE p.protocol = 'suntech' AND d.lastupdate > localtimestamp - interval '7 days'
ORDER BY gap DESC NULLS FIRST LIMIT 50;
```

Server log check (shell): `grep -c 'Latitude out of range' /opt/traccar/logs/tracker-server.log*`.

### 4c. Atrack custom format and other per-protocol settings

From the production config, list keys only (values may be secret):

```sh
grep -oE "key=['\"][^'\"]+['\"]" /opt/traccar/conf/traccar.xml | sort
```

For `atrack.*` (`atrack.custom`, `atrack.form`, `atrack.longDate`, ...), `suntech.*`,
`intellitrac.*` and `osmand.*`, the values are not secret, so copy them in full. Note that
`suntech_udp.port` (in `sir-dev.xml`) has no effect: no protocol named `suntech_udp` exists, and
Java opens no UDP Suntech port. Check whether production devices send Suntech over UDP:
`ss -lunp | grep java`.

### 4d. Position attributes per protocol (decides which decoder paths matter)

**(heavy)** One day of data is enough:

```sql
SELECT p.protocol, k AS attribute, count(*)
FROM tc_positions p, json_object_keys(p.attributes::json) AS k
WHERE p.fixtime > localtimestamp - interval '1 day' AND p.attributes LIKE '{%'
GROUP BY p.protocol, k ORDER BY p.protocol, count(*) DESC;

SELECT protocol, count(*) FILTER (WHERE network IS NOT NULL AND network <> 'null') AS with_network,
       count(*) FILTER (WHERE geofenceids IS NOT NULL AND geofenceids <> 'null') AS with_geofences,
       count(*) FILTER (WHERE address IS NOT NULL) AS with_address
FROM tc_positions WHERE fixtime > localtimestamp - interval '1 day' GROUP BY protocol;
```

## 5. Features in use (decides the list of features to port)

```sql
-- Notifications: event types × channels.
SELECT type, notificators, always, count(*) FROM tc_notifications
GROUP BY type, notificators, always ORDER BY count(*) DESC;

-- Computed attributes: export in full. Every expression must be audited for JEXL→expr.
SELECT id, description, attribute, type, priority, expression FROM tc_attributes ORDER BY id;
SELECT 'device' AS via, count(*) FROM tc_device_attribute
UNION ALL SELECT 'group', count(*) FROM tc_group_attribute
UNION ALL SELECT 'user', count(*) FROM tc_user_attribute;

-- Scheduled reports, saved commands, command queue.
SELECT type, count(*) FROM tc_reports GROUP BY type;
SELECT type, textchannel, count(*) FROM tc_commands GROUP BY type, textchannel;
SELECT type, count(*) FROM tc_commands_queue GROUP BY type;

-- Object counts and sizes.
SELECT relname, n_live_tup, pg_size_pretty(pg_total_relation_size(relid)) AS size
FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC;

-- Geofences: shape types and size (TEXT vs VARCHAR(4096), see section 1).
SELECT split_part(area, ' ', 1) AS shape, count(*), max(length(area)) AS max_len
FROM tc_geofences GROUP BY 1;

-- Calendars, drivers, maintenance, orders: used at all?
SELECT (SELECT count(*) FROM tc_calendars) AS calendars, (SELECT count(*) FROM tc_drivers) AS drivers,
       (SELECT count(*) FROM tc_maintenances) AS maintenances, (SELECT count(*) FROM tc_orders) AS orders,
       (SELECT count(*) FROM tc_device_device) AS linked_devices;

-- Event detectors that actually fire (heavy if tc_events is large; 30 days).
SELECT type, count(*) FROM tc_events
WHERE eventtime > localtimestamp - interval '30 days' GROUP BY type ORDER BY count(*) DESC;
SELECT attributes::json->>'alarm' AS alarm, count(*) FROM tc_events
WHERE type = 'alarm' AND attributes LIKE '{%' AND eventtime > localtimestamp - interval '30 days' GROUP BY 1 ORDER BY 2 DESC;
```

## 6. Users, auth and sessions (decides Step 3 scope)

```sql
SELECT count(*) AS users,
       count(*) FILTER (WHERE administrator) AS admins,
       count(*) FILTER (WHERE userlimit <> 0) AS managers,
       count(*) FILTER (WHERE readonly) AS readonly,
       count(*) FILTER (WHERE devicereadonly) AS device_readonly,
       count(*) FILTER (WHERE disabled) AS disabled,
       count(*) FILTER (WHERE temporary) AS temporary,
       count(*) FILTER (WHERE totpkey IS NOT NULL) AS totp,
       count(*) FILTER (WHERE login IS NOT NULL) AS with_login,       -- LDAP / OpenID
       count(*) FILTER (WHERE hashedpassword IS NULL) AS no_password  -- LDAP / OpenID only
FROM tc_users;

SELECT count(*) AS keys FROM tc_keystore;          -- Go must reuse these keys for bearer tokens.
SELECT count(*) AS revoked FROM tc_revoked_tokens;

-- Attribute keys used on users/devices/groups/server (preferences, notification tokens, limits).
-- The LIKE guard skips empty or invalid values, which would abort json_object_keys.
SELECT 'user' AS owner, k, count(*) FROM tc_users, json_object_keys(attributes::json) k
  WHERE attributes LIKE '{%' GROUP BY k
UNION ALL SELECT 'device', k, count(*) FROM tc_devices, json_object_keys(attributes::json) k
  WHERE attributes LIKE '{%' GROUP BY k
UNION ALL SELECT 'group', k, count(*) FROM tc_groups, json_object_keys(attributes::json) k
  WHERE attributes LIKE '{%' GROUP BY k
ORDER BY 1, 3 DESC;
SELECT attributes FROM tc_servers;  -- review by hand; may contain keys/URLs

-- Audit log: which actions happen (30 days).
SELECT actiontype, objecttype, count(*) FROM tc_actions
WHERE actiontime > localtimestamp - interval '30 days' GROUP BY 1, 2 ORDER BY 3 DESC;
```

Two Java defaults matter even when the key is absent:
- `server.statistics` defaults to `https://www.traccar.org/analytics/`. Unless production sets it
  to an empty value, Java posts daily usage counts to traccar.org. `sir-dev.xml` doesn't set it.
  Decide whether Go reproduces this.
- `server.buffering.threshold` defaults to 3000 ms, not 0. `sir-dev.xml` sets 5000. Record the
  production value: fixes are held this long before processing, and the ordering depends on it.

Config keys to check (names only, from 4c): `openid.*`, `ldap.*`, `web.serviceAccountToken`,
`web.sanitize`, `web.origin`, `server.statistics`, `notificator.*`, `forward.*`, `event.forward.*`,
`geocoder.*`, `geolocation.*`, `report.*`, `database.saveOriginal`, `database.registerUnknown*`,
`status.timeout`, `server.timeout`, `filter.*`, `coordinates.*`, `processing.*`, `time.*`.

## 7. API endpoints in use (decides Steps 4 and 5 scope)

If `web.requestLog.path` is set, Jetty writes NCSA-style lines
(`host - user [date] "METHOD URI PROTO" status bytes`). Otherwise, use the reverse proxy's
access log (nginx/Apache) for the API vhost. Aggregate 30 days, with numeric IDs replaced:

```sh
cat jetty-*.request.log | awk '{print $6, $7}' | tr -d '"' \
  | sed -E 's/\?.*$//; s#/[0-9]+(/|$)#/{id}\1#g' \
  | sort | uniq -c | sort -rn > endpoints.txt
```

Split the result by client where possible (User-Agent or vhost: SIR-web, SIR-manager,
SIR-client). Also list query parameter **names** in use per endpoint (no values):

```sh
cat jetty-*.request.log | awk '{print $7}' | grep '?' \
  | sed -E 's#/[0-9]+(/|$)#/{id}\1#g; s/=[^&]*//g' | sort | uniq -c | sort -rn > endpoint-params.txt
```

Also check WebSocket usage (`/api/socket`) and whether clients use cookie sessions, bearer
tokens or Basic auth (count `Authorization` header schemes at the proxy if it logs them).

## 8. Secrets (Step 0 action item, not a query)

`sir-dev.xml` in git holds database, SMTP and webhook credentials. Rotate them and move them
out of git (environment variables via `CONFIG_USE_ENVIRONMENT_VARIABLES`, or an untracked
file). The Go server reads the same keys and environment overrides.
