-- Step 0 inventory, heavy queries: these scan tc_positions / tc_events. Read-only.
-- Run off-peak or on a replica:  psql -X ... -d traccar_prd -f step0-heavy.sql > step0-heavy-results.txt 2>&1

SET default_transaction_read_only = on;
SET statement_timeout = '600s';
SET lock_timeout = '5s';
SET TimeZone = 'America/Sao_Paulo';
\pset pager off

\echo '== 4. Device and position counts per protocol, 90 days'
SELECT protocol, count(DISTINCT deviceid) AS devices, count(*) AS positions,
       min(fixtime) AS first_fix, max(fixtime) AS last_fix
FROM tc_positions WHERE fixtime > localtimestamp - interval '90 days'
GROUP BY protocol ORDER BY devices DESC;

\echo '== 4d. Position attributes per protocol, 1 day'
SELECT p.protocol, k AS attribute, count(*)
FROM tc_positions p, json_object_keys(p.attributes::json) AS k
WHERE p.fixtime > localtimestamp - interval '1 day' AND p.attributes LIKE '{%'
GROUP BY p.protocol, k ORDER BY p.protocol, count(*) DESC;
SELECT protocol, count(*) FILTER (WHERE network IS NOT NULL AND network <> 'null') AS with_network,
       count(*) FILTER (WHERE geofenceids IS NOT NULL AND geofenceids <> 'null') AS with_geofences,
       count(*) FILTER (WHERE address IS NOT NULL) AS with_address
FROM tc_positions WHERE fixtime > localtimestamp - interval '1 day' GROUP BY protocol;

\echo '== 5. Events that fire, 30 days'
SELECT type, count(*) FROM tc_events
WHERE eventtime > localtimestamp - interval '30 days' GROUP BY type ORDER BY count(*) DESC;
SELECT attributes::json->>'alarm' AS alarm, count(*) FROM tc_events
WHERE type = 'alarm' AND attributes LIKE '{%' AND eventtime > localtimestamp - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;
