-- Step 0 inventory, cheap queries. Read-only; safe on production.
-- Run:  psql -X -h HOST -U USER -d traccar_prd -f step0.sql > step0-results.txt 2>&1
-- See README.md for what each section decides.

SET default_transaction_read_only = on;   -- any write fails instead of running
SET statement_timeout = '120s';
SET lock_timeout = '5s';
-- Timestamps hold the JVM's wall-clock time. Change this if section 2 shows another zone.
SET TimeZone = 'America/Sao_Paulo';
\pset pager off

\echo '== 1. Schema version'
SELECT id, author, filename, dateexecuted, orderexecuted, exectype, md5sum, liquibase
FROM databasechangelog ORDER BY orderexecuted;
SELECT * FROM databasechangeloglock;
SELECT table_name, column_name, data_type, character_maximum_length, is_nullable, column_default
FROM information_schema.columns WHERE table_schema = 'public' ORDER BY table_name, ordinal_position;
SELECT extname, extversion FROM pg_extension;
SELECT version();

\echo '== 2. Time zone'
SHOW TimeZone;
SHOW DateStyle;
SELECT now(), localtimestamp;
SELECT max(servertime) AS newest_servertime,
       now() AT TIME ZONE 'UTC' AS utc_now,
       now() AT TIME ZONE 'America/Sao_Paulo' AS sao_paulo_now
FROM tc_positions WHERE id > (SELECT max(id) - 1000 FROM tc_positions);

\echo '== 4. Protocols in use (latest fix of each device active in 90 days)'
SELECT p.protocol, count(*) AS devices
FROM tc_devices d JOIN tc_positions p ON p.id = d.positionid
WHERE d.lastupdate > localtimestamp - interval '90 days'
GROUP BY p.protocol ORDER BY devices DESC;
SELECT capturetime, activedevices, messagesreceived, messagesstored, protocols, requests
FROM tc_statistics ORDER BY capturetime DESC LIMIT 30;

\echo '== 4b. Suntech devices online but with a stale last fix'
SELECT d.id, d.uniqueid, d.status, d.lastupdate, p.fixtime, p.servertime,
       d.lastupdate - p.servertime AS gap
FROM tc_devices d LEFT JOIN tc_positions p ON p.id = d.positionid
WHERE p.protocol = 'suntech' AND d.lastupdate > localtimestamp - interval '7 days'
ORDER BY gap DESC NULLS FIRST LIMIT 50;

\echo '== 5. Features in use'
SELECT type, notificators, always, count(*) FROM tc_notifications
GROUP BY type, notificators, always ORDER BY count(*) DESC;
SELECT id, description, attribute, type, priority, expression FROM tc_attributes ORDER BY id;
SELECT 'device' AS via, count(*) FROM tc_device_attribute
UNION ALL SELECT 'group', count(*) FROM tc_group_attribute
UNION ALL SELECT 'user', count(*) FROM tc_user_attribute;
SELECT type, count(*) FROM tc_reports GROUP BY type;
SELECT type, textchannel, count(*) FROM tc_commands GROUP BY type, textchannel;
SELECT type, count(*) FROM tc_commands_queue GROUP BY type;
SELECT relname, n_live_tup, pg_size_pretty(pg_total_relation_size(relid)) AS size
FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC;
SELECT split_part(area, ' ', 1) AS shape, count(*), max(length(area)) AS max_len
FROM tc_geofences GROUP BY 1;
SELECT (SELECT count(*) FROM tc_calendars) AS calendars, (SELECT count(*) FROM tc_drivers) AS drivers,
       (SELECT count(*) FROM tc_maintenances) AS maintenances, (SELECT count(*) FROM tc_orders) AS orders,
       (SELECT count(*) FROM tc_device_device) AS linked_devices;

\echo '== 6. Users, auth and sessions'
SELECT count(*) AS users,
       count(*) FILTER (WHERE administrator) AS admins,
       count(*) FILTER (WHERE userlimit <> 0) AS managers,
       count(*) FILTER (WHERE readonly) AS readonly,
       count(*) FILTER (WHERE devicereadonly) AS device_readonly,
       count(*) FILTER (WHERE disabled) AS disabled,
       count(*) FILTER (WHERE temporary) AS temporary,
       count(*) FILTER (WHERE totpkey IS NOT NULL) AS totp,
       count(*) FILTER (WHERE login IS NOT NULL) AS with_login,
       count(*) FILTER (WHERE hashedpassword IS NULL) AS no_password
FROM tc_users;
SELECT count(*) AS keys FROM tc_keystore;
SELECT count(*) AS revoked FROM tc_revoked_tokens;
SELECT 'user' AS owner, k, count(*) FROM tc_users, json_object_keys(attributes::json) k
  WHERE attributes LIKE '{%' GROUP BY k
UNION ALL SELECT 'device', k, count(*) FROM tc_devices, json_object_keys(attributes::json) k
  WHERE attributes LIKE '{%' GROUP BY k
UNION ALL SELECT 'group', k, count(*) FROM tc_groups, json_object_keys(attributes::json) k
  WHERE attributes LIKE '{%' GROUP BY k
ORDER BY 1, 3 DESC;
-- Review by hand before sharing; may contain API keys or URLs.
SELECT attributes FROM tc_servers;
SELECT actiontype, objecttype, count(*) FROM tc_actions
WHERE actiontime > localtimestamp - interval '30 days' GROUP BY 1, 2 ORDER BY 3 DESC;
