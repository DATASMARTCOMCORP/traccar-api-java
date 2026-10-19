package org.traccar.integration;

import com.fasterxml.jackson.databind.JsonNode;
import org.junit.jupiter.api.Test;
import org.traccar.model.Event;
import org.traccar.schedule.TaskOutbox;

import java.sql.Connection;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;
import java.util.stream.IntStream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * One transaction per position (device lock, state reload, position, receipt, device state, events and outbox
 * together), deliveries after commit with recovery, and acceptance policy shared by both ingestion paths.
 */
class CrashSafeProcessingTest extends IngestionTestSupport {

    private static final int MOVING = 21;
    private static final int TOTAL = 27;

    private String driveItem(int k) {
        int point = Math.min(k, MOVING - 1);
        return batchItem(time(k), routeLat(point), CENTER_LON, k < MOVING ? 30 : 0, null);
    }

    private List<String> driveItems(int from, int to) {
        return IntStream.range(from, to).mapToObj(this::driveItem).toList();
    }

    private static List<String> statuses(java.net.http.HttpResponse<String> response) throws Exception {
        var result = new ArrayList<String>();
        for (JsonNode item : body(response).get("results")) {
            result.add(item.get("status").asText());
        }
        return result;
    }

    private void sql(String statement) throws Exception {
        try (var connection = connect(); var jdbc = connection.createStatement()) {
            jdbc.execute(statement);
        }
    }

    private long count(String query) throws Exception {
        try (var connection = connect(); var jdbc = connection.createStatement(); var rs = jdbc.executeQuery(query)) {
            rs.next();
            return rs.getLong(1);
        }
    }

    @Test
    void eventsCommitWithTheirPositionOrNotAtAll() throws Exception {
        var server = start();
        var fixture = seed(server, "atomic", Map.of());
        sql("CREATE FUNCTION injected_failure() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected failure';"
                + " END $$ LANGUAGE plpgsql");
        sql("CREATE TRIGGER injected_failure BEFORE INSERT ON tc_events FOR EACH ROW WHEN (NEW.type = '"
                + Event.TYPE_GEOFENCE_ENTER + "') EXECUTE FUNCTION injected_failure()");

        String body = batch(fixture, driveItems(0, 15));
        var first = server.batch(body);

        // Storing the enter event failed, so its position was not stored either: nothing half done
        assertEquals(503, first.statusCode());
        assertEquals("failed", statuses(first).get(10));
        assertEquals(14, positions(fixture.deviceId()).size());
        assertTrue(positions(fixture.deviceId()).stream().noneMatch(p -> p.latitude() == routeLat(10)));
        assertEquals(List.of(), eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(0, count("SELECT count(*) FROM tc_outbox WHERE deviceid = " + fixture.deviceId()
                + " AND objectid NOT IN (SELECT id FROM tc_events)"), "no delivery without its event");

        sql("DROP TRIGGER injected_failure ON tc_events");
        var retry = server.batch(body);
        assertEquals(200, retry.statusCode());
        assertEquals("stored", statuses(retry).get(10));
        // The retried point is late now; its visit is still reported, at the right positions
        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(11)),
                eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
    }

    @Test
    void undeliveredWebhooksAreRecoveredAfterACrashAndCarryStableIds() throws Exception {
        webhookStatus = 500;
        var crashed = start();
        var fixture = seed(crashed, "webhook", Map.of());
        assertEquals(200, crashed.batch(batch(fixture, driveItems(0, TOTAL))).statusCode());
        var events = events(fixture.deviceId());
        assertTrue(events.size() >= 4);
        String pendingEvents = "SELECT count(*) FROM tc_outbox WHERE kind = 'event' AND deliveredat IS NULL";
        await(() -> {
            try {
                // every failed attempt pushed its claim into the future
                return count(pendingEvents + " AND claimeduntil > now()") == events.size();
            } catch (Exception e) {
                throw new RuntimeException(e);
            }
        }, "failed webhook attempts");
        assertEquals(0, forwarded.size());
        crashed.close();
        instances.remove(crashed);

        // Another server takes over once the retry delay has passed
        webhookStatus = 200;
        sql("UPDATE tc_outbox SET claimeduntil = NULL WHERE deliveredat IS NULL");
        var survivor = start();
        survivor.get(TaskOutbox.class).run();
        awaitForwarded(events.size());

        var forwardedIds = forwarded.stream().map(f -> f.at("/event/id").asLong()).collect(Collectors.toSet());
        assertEquals(events.size(), forwardedIds.size());
        assertEquals(0, count(pendingEvents));

        // A crash after sending but before marking it sent repeats the delivery, with the same event id
        sql("UPDATE tc_outbox SET deliveredat = NULL, claimeduntil = NULL WHERE kind = 'event' AND id = "
                + "(SELECT min(id) FROM tc_outbox WHERE kind = 'event')");
        survivor.get(TaskOutbox.class).run();
        awaitForwarded(events.size() + 1);
        var all = forwarded.stream().map(f -> f.at("/event/id").asLong()).toList();
        assertEquals(events.size() + 1, all.size());
        assertEquals(forwardedIds, new HashSet<>(all), "the repeat is recognizable by its event id");
    }

    @Test
    void serversTakingTurnsBehaveLikeOneServer() throws Exception {
        var first = startSynchronous();
        var second = startSynchronous();
        var fixture = seed(first, "turns", Map.of("speedLimit", 25.0));

        // Every other position goes to the other server; each one reloads what the previous one committed
        for (int k = 0; k < TOTAL; k++) {
            int point = Math.min(k, MOVING - 1);
            var server = k % 2 == 0 ? first : second;
            assertEquals(200, server.live("id=turns&lat=" + routeLat(point) + "&lon=" + CENTER_LON
                    + "&timestamp=" + time(k) + "&speed=" + (k < MOVING ? 30 : 0)).statusCode());
        }

        var stored = positions(fixture.deviceId());
        assertEquals(TOTAL, stored.size());
        assertEquals(stored.getLast().id(), latestPositionId(fixture.deviceId()));
        for (int k = 1; k < TOTAL; k++) {
            assertTrue(stored.get(k).totalDistance() >= stored.get(k - 1).totalDistance());
        }
        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(11)),
                eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(List.of("deviceOverspeed@" + routeLat(0)),
                eventTrail(fixture.deviceId(), Event.TYPE_DEVICE_OVERSPEED));
        var motion = eventTrail(fixture.deviceId(), Event.TYPE_DEVICE_MOVING, Event.TYPE_DEVICE_STOPPED);
        assertEquals(2, motion.size(), motion.toString());
        assertEquals("deviceStopped@" + routeLat(MOVING - 1), motion.get(1));
    }

    @Test
    void aStuckOwnerBlocksOnlyUntilItsConnectionIsGone() throws Exception {
        var server = start(Map.of("processing.lockTimeout", "300"));
        var fixture = seed(server, "stuck", Map.of());
        String body = batch(fixture, driveItems(0, 3));

        Connection owner = connect();
        owner.setAutoCommit(false);
        owner.createStatement().execute("SELECT pg_advisory_xact_lock(" + 0x7472 + ", " + fixture.deviceId() + ")");

        var blocked = server.batch(body);
        assertEquals(503, blocked.statusCode());
        assertEquals(List.of("failed", "failed", "failed"), statuses(blocked));
        assertEquals(0, positions(fixture.deviceId()).size());

        owner.close(); // the owner dies without committing: its lock goes with the connection

        var retry = server.batch(body);
        assertEquals(200, retry.statusCode());
        assertEquals(List.of("stored", "stored", "stored"), statuses(retry));
        assertEquals(3, positions(fixture.deviceId()).size());
    }

    @Test
    void reportsOutsideTheAcceptedAgeAreQuarantinedOnBothPaths() throws Exception {
        var server = startSynchronous();
        var fixture = seed(server, "aged", Map.of());
        var old = base.minus(Duration.ofDays(8));
        String liveOld = "id=aged&lat=" + routeLat(0) + "&lon=" + CENTER_LON + "&timestamp=" + old;

        assertEquals(200, server.live(liveOld).statusCode());
        assertEquals(200, server.live(liveOld).statusCode());
        var batch = server.batch(batch(fixture, List.of(
                batchItem(old, routeLat(0), CENTER_LON, 0, null),
                batchItem(base.plus(Duration.ofDays(2)), routeLat(1), CENTER_LON, 0, "ahead"))));
        assertEquals(List.of("quarantined", "quarantined"), statuses(batch));
        assertEquals("too old", body(batch).at("/results/0/reason").asText());
        assertEquals("future", body(batch).at("/results/1/reason").asText());

        assertEquals(0, positions(fixture.deviceId()).size());
        assertEquals(2, count("SELECT count(*) FROM tc_position_quarantine WHERE deviceid = " + fixture.deviceId()),
                "the old report sent three times is kept once, next to the future one");
        assertEquals(1, count("SELECT count(*) FROM tc_position_quarantine WHERE reason = 'too old'"
                + " AND position LIKE '%" + routeLat(0) + "%'"));
    }

    @Test
    void latePositionsRecordTheIntervalTheyChange() throws Exception {
        var server = start();
        var fixture = seed(server, "gaps", Map.of());
        server.batch(batch(fixture, driveItems(18, TOTAL)));
        assertEquals(0, count("SELECT count(*) FROM tc_late_positions"));

        server.batch(batch(fixture, driveItems(9, 12)));

        try (var connection = connect(); var statement = connection.prepareStatement(
                "SELECT fromtime, totime FROM tc_late_positions WHERE deviceid = ? AND completedat IS NULL"
                        + " ORDER BY fromtime")) {
            statement.setLong(1, fixture.deviceId());
            try (var rs = statement.executeQuery()) {
                var gaps = new ArrayList<String>();
                while (rs.next()) {
                    gaps.add(rs.getTimestamp(1).toInstant() + ".." + rs.getTimestamp(2).toInstant());
                }
                assertEquals(List.of(
                        time(9) + ".." + time(18), time(9) + ".." + time(18), time(10) + ".." + time(18)), gaps);
            }
        }
    }

    @Test
    void positionsAreForwardedOnceAfterTheyAreStored() throws Exception {
        var server = start(Map.of("forward.type", "json", "forward.url", positionsUrl()));
        var fixture = seed(server, "forwarded", Map.of());
        String body = batch(fixture, new ArrayList<>(List.of(driveItem(0), driveItem(1),
                batchItem(time(2), 0, 0, 0, null)))); // (0, 0) is filtered

        server.batch(body);
        server.batch(body); // a redelivery

        var stored = positions(fixture.deviceId()).stream().map(StoredRow::id).collect(Collectors.toSet());
        assertEquals(2, stored.size());
        await(() -> forwardedPositions.size() >= 2, "forwarded positions");
        Set<Long> forwardedIds = forwardedPositions.stream()
                .map(f -> f.at("/position/id").asLong()).collect(Collectors.toSet());
        assertEquals(stored, forwardedIds, "stored positions, with their ids; no filtered one, no duplicate");
        await(() -> {
            try {
                return count("SELECT count(*) FROM tc_outbox WHERE kind = 'position' AND deliveredat IS NULL") == 0;
            } catch (Exception e) {
                throw new RuntimeException(e);
            }
        }, "position deliveries marked done");
        assertEquals(2, forwardedPositions.size());
    }

}
