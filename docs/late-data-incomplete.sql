-- Derived results that may be incomplete because positions arrived late and nothing has reconstructed
-- them yet (tc_late_positions.completedat stays NULL until a reconstruction job exists and runs).
--
-- One row per device and merged interval of late data, oldest first, with the derived events stored inside
-- the interval. Inside such an interval:
--   * geofence visits hidden in the gap were reported (events with "late": true), but a closing event may be
--     attached to the first position after the gap rather than to the exact crossing;
--   * motion (deviceMoving/deviceStopped), overspeed and ignition events were NOT recomputed;
--   * trips/stops reports over more than report.fastThreshold (1 day) read motion events, so they can be
--     wrong; shorter ranges recompute from positions and include the late ones;
--   * summary distance may undercount: totals of positions stored before the late ones are not rewritten.
WITH pending AS (
    SELECT deviceid, fromtime, totime, createdat
    FROM tc_late_positions
    WHERE completedat IS NULL
), ordered AS (
    SELECT *, max(totime) OVER (PARTITION BY deviceid ORDER BY fromtime, totime
                                ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS previousend
    FROM pending
), islands AS (
    SELECT *, sum(CASE WHEN previousend IS NULL OR fromtime > previousend THEN 1 ELSE 0 END)
                  OVER (PARTITION BY deviceid ORDER BY fromtime, totime) AS island
    FROM ordered
), intervals AS (
    SELECT deviceid, min(fromtime) AS fromtime, max(totime) AS totime,
           count(*) AS latepositions, min(createdat) AS pendingsince
    FROM islands
    GROUP BY deviceid, island
)
SELECT i.deviceid,
       d.uniqueid,
       i.fromtime,
       i.totime,
       i.latepositions,
       i.pendingsince,
       (SELECT count(*) FROM tc_events e
        WHERE e.deviceid = i.deviceid AND e.eventtime BETWEEN i.fromtime AND i.totime
          AND e.type IN ('deviceMoving', 'deviceStopped', 'deviceOverspeed', 'ignitionOn', 'ignitionOff'))
           AS unreconstructedevents,
       (SELECT count(*) FROM tc_events e
        WHERE e.deviceid = i.deviceid AND e.eventtime BETWEEN i.fromtime AND i.totime
          AND e.type IN ('geofenceEnter', 'geofenceExit') AND e.attributes LIKE '%"late":true%')
           AS lategeofenceevents
FROM intervals i
JOIN tc_devices d ON d.id = i.deviceid
ORDER BY i.pendingsince, i.deviceid, i.fromtime;

-- Is a report for one device and range affected? (bind :deviceid, :from, :to)
-- SELECT EXISTS (SELECT 1 FROM tc_late_positions
--                WHERE deviceid = :deviceid AND completedat IS NULL AND fromtime <= :to AND totime >= :from);
