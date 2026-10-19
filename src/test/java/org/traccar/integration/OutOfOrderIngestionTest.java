package org.traccar.integration;

import com.fasterxml.jackson.databind.JsonNode;
import org.junit.jupiter.api.Test;
import org.traccar.helper.DistanceCalculator;
import org.traccar.model.Event;

import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.Callable;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.stream.IntStream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Batch ingestion as the queue consumer uses it: durable per-item outcomes, idempotent redelivery, reordering
 * within and across batches, late reports, concurrency, failures and several servers on one database.
 */
class OutOfOrderIngestionTest extends IngestionTestSupport {

    private static final int MOVING = 21;
    private static final int TOTAL = 27;

    /** The ordered drive of OrderedTrafficTest, as batch items: 21 moving points, then 6 parked ones. */
    private String driveItem(int k) {
        int point = Math.min(k, MOVING - 1);
        return batchItem(time(k), routeLat(point), CENTER_LON, k < MOVING ? 30 : 0, null);
    }

    private List<String> driveItems(int from, int to) {
        return IntStream.range(from, to).mapToObj(this::driveItem).toList();
    }

    private static List<String> statuses(HttpResponse<String> response) throws Exception {
        var result = new ArrayList<String>();
        for (JsonNode item : body(response).get("results")) {
            result.add(item.get("status").asText());
        }
        return result;
    }

    private static List<String> repeat(String value, int count) {
        return Collections.nCopies(count, value);
    }

    private double routeLength() {
        double route = 0;
        for (int k = 1; k < MOVING; k++) {
            route += DistanceCalculator.distance(routeLat(k - 1), CENTER_LON, routeLat(k), CENTER_LON);
        }
        return route;
    }

    private void failInsertsAt(long deviceId, double latitude) throws Exception {
        try (var connection = connect(); var statement = connection.createStatement()) {
            statement.execute("CREATE OR REPLACE FUNCTION injected_failure() RETURNS trigger AS $$ BEGIN "
                    + "RAISE EXCEPTION 'injected failure'; END $$ LANGUAGE plpgsql");
            statement.execute("CREATE TRIGGER injected_failure BEFORE INSERT ON tc_positions FOR EACH ROW WHEN "
                    + "(NEW.deviceid = " + deviceId + " AND abs(NEW.latitude - (" + latitude + ")) < 1e-9) "
                    + "EXECUTE FUNCTION injected_failure()");
        }
    }

    private void repairInserts() throws Exception {
        try (var connection = connect(); var statement = connection.createStatement()) {
            statement.execute("DROP TRIGGER injected_failure ON tc_positions");
        }
    }

    @Test
    void shuffledBatchMatchesOrderedLiveTraffic() throws Exception {
        var server = start();
        var fixture = seed(server, "shuffled", Map.of("speedLimit", 25.0));
        server.watch(fixture.userId());

        var items = new ArrayList<>(driveItems(0, TOTAL));
        Collections.shuffle(items, new Random(7));
        var response = server.batch(batch(fixture, items));

        assertEquals(200, response.statusCode());
        assertEquals(TOTAL, body(response).get("accepted").asInt());
        assertEquals(repeat("stored", TOTAL), statuses(response));

        // The reply comes after processing, so everything below is already visible
        var stored = positions(fixture.deviceId());
        assertEquals(TOTAL, stored.size());
        for (int k = 0; k < TOTAL; k++) {
            assertEquals(time(k), stored.get(k).fixTime());
        }
        assertEquals(stored.getLast().id(), latestPositionId(fixture.deviceId()));
        assertEquals(routeLength(), stored.getLast().totalDistance(), 0.01);
        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(11)),
                eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(List.of("deviceOverspeed@" + routeLat(0)),
                eventTrail(fixture.deviceId(), Event.TYPE_DEVICE_OVERSPEED));
        var motion = eventTrail(fixture.deviceId(), Event.TYPE_DEVICE_MOVING, Event.TYPE_DEVICE_STOPPED);
        assertEquals(2, motion.size(), motion.toString());
        assertEquals("deviceStopped@" + routeLat(MOVING - 1), motion.get(1));
        assertEquals(TOTAL, server.socketPositions.size());

        // Every result names the stored row
        var ids = new HashSet<Long>();
        for (JsonNode item : body(response).get("results")) {
            ids.add(item.get("positionId").asLong());
        }
        assertEquals(stored.stream().map(StoredRow::id).collect(java.util.stream.Collectors.toSet()), ids);
    }

    @Test
    void batchesArrivingInReverseKeepTheNewestCurrentAndReportTheMissedVisit() throws Exception {
        var server = start();
        var fixture = seed(server, "reverse", Map.of());
        server.watch(fixture.userId());

        // Three consecutive batches delivered newest first, as a queue may reorder them
        assertEquals(200, server.batch(batch(fixture, driveItems(18, TOTAL))).statusCode());
        long newest = latestPositionId(fixture.deviceId());
        int pushed = server.socketPositions.size();
        assertEquals(200, server.batch(batch(fixture, driveItems(0, 9))).statusCode());
        assertEquals(200, server.batch(batch(fixture, driveItems(9, 18))).statusCode());

        var stored = positions(fixture.deviceId());
        assertEquals(TOTAL, stored.size());
        for (int k = 0; k < TOTAL; k++) {
            assertEquals(time(k), stored.get(k).fixTime(), "history keeps the original fix times");
        }
        assertEquals(newest, latestPositionId(fixture.deviceId()), "late batches never replace the newest");
        assertEquals(time(TOTAL - 1), fixTime(newest));
        assertEquals(pushed, server.socketPositions.size(), "late positions are not pushed as current");

        // The geofence was entered and left inside the late gap: both crossings are reported, marked late.
        // The exit is attached to the first position stored after the gap (point 18), not to point 11.
        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(18)),
                eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertTrue(events(fixture.deviceId()).stream()
                .filter(e -> e.type().startsWith("geofence")).allMatch(EventRow::late));
        assertEquals(2, server.socketEvents.stream().filter(e -> e.getType().startsWith("geofence")).count());

        // Totals stay non-decreasing along fix time; late positions never push them past later ones
        for (int k = 1; k < TOTAL; k++) {
            assertTrue(stored.get(k).totalDistance() >= stored.get(k - 1).totalDistance() - 1e-6);
        }
    }

    @Test
    void lateVisitBetweenTwoLivePositions() throws Exception {
        var server = start();
        var fixture = seed(server, "visit", Map.of());
        server.watch(fixture.userId());

        server.live(liveQuery(fixture, 8, 30));
        server.live(liveQuery(fixture, 12, 30));
        // The batch reply also proves that the earlier live positions of this device were processed
        var response = server.batch(batch(fixture, List.of(routeItem(11, 30), routeItem(9, 30), routeItem(10, 30))));
        assertEquals(repeat("stored", 3), statuses(response));

        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(12)),
                eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(time(12), fixTime(latestPositionId(fixture.deviceId())));

        var lateEvents = server.socketEvents.stream().filter(e -> e.getType().startsWith("geofence")).toList();
        assertEquals(2, lateEvents.size());
        assertTrue(lateEvents.stream().allMatch(e -> e.getBoolean(Event.KEY_LATE)));
        awaitForwarded(events(fixture.deviceId()).size());
        assertTrue(forwarded.stream().filter(f -> f.at("/event/type").asText().startsWith("geofence"))
                .allMatch(f -> f.at("/event/attributes/late").asBoolean()));

        // A late point between two inside points changes nothing that was reported
        var stored = positions(fixture.deviceId());
        double previousTotal = stored.get(2).totalDistance(); // point 10
        assertEquals(stored.get(1).totalDistance() + DistanceCalculator.distance(
                routeLat(9), CENTER_LON, routeLat(10), CENTER_LON), previousTotal, 0.01);
    }

    @Test
    void equalTimestampsAndConflictingReports() throws Exception {
        var server = start();
        var fixture = seed(server, "ties", Map.of());

        // Two different reports with the same fix time, in either order: both are history, and the current
        // position is the one stored last (higher id), so the outcome depends only on what is stored
        var first = server.batch(batch(fixture, List.of(batchItem(time(5), routeLat(1), CENTER_LON, 0, "a"))));
        var second = server.batch(batch(fixture, List.of(batchItem(time(5), routeLat(2), CENTER_LON, 0, "b"))));
        long firstId = body(first).at("/results/0/positionId").asLong();
        long secondId = body(second).at("/results/0/positionId").asLong();
        assertTrue(secondId > firstId);
        assertEquals(secondId, latestPositionId(fixture.deviceId()));

        // The same id with different content is the same report: the first stored version stays
        var conflict = server.batch(batch(fixture, List.of(batchItem(time(5), routeLat(3), CENTER_LON, 0, "a"))));
        assertEquals("duplicate", body(conflict).at("/results/0/status").asText());
        assertEquals(firstId, body(conflict).at("/results/0/positionId").asLong());

        // Without ids, the fix (time and coordinates) is the identity: live and batch copies are one report
        assertEquals(200, server.live(liveQuery(fixture, 6, 0)).statusCode());
        var copy = server.batch(batch(fixture, List.of(routeItem(6, 0), routeItem(6, 0))));
        assertEquals(List.of("duplicate", "duplicate"), statuses(copy));
        assertEquals(3, positions(fixture.deviceId()).size());
    }

    @Test
    void concurrentDuplicateDeliveriesToTwoServersStoreOnce() throws Exception {
        var first = start();
        var second = start();
        var fixture = seed(first, "racing", Map.of());

        var items = driveItems(0, TOTAL);
        String body = batch(fixture, items);
        var ready = new CountDownLatch(2);
        ExecutorService executor = Executors.newFixedThreadPool(2);
        try {
            Callable<HttpResponse<String>> a = () -> {
                ready.countDown();
                ready.await();
                return first.batch(body);
            };
            Callable<HttpResponse<String>> b = () -> {
                ready.countDown();
                ready.await();
                return second.batch(body);
            };
            Future<HttpResponse<String>> ra = executor.submit(a);
            Future<HttpResponse<String>> rb = executor.submit(b);
            var sa = statuses(ra.get());
            var sb = statuses(rb.get());
            assertEquals(200, ra.get().statusCode());
            assertEquals(200, rb.get().statusCode());
            for (int i = 0; i < TOTAL; i++) {
                var pair = new HashSet<>(List.of(sa.get(i), sb.get(i)));
                assertEquals(new HashSet<>(List.of("stored", "duplicate")), pair, "item " + i);
                assertEquals(body(ra.get()).at("/results/" + i + "/positionId").asLong(),
                        body(rb.get()).at("/results/" + i + "/positionId").asLong());
            }
        } finally {
            executor.shutdown();
        }

        var stored = positions(fixture.deviceId());
        assertEquals(TOTAL, stored.size());
        assertEquals(time(TOTAL - 1), fixTime(latestPositionId(fixture.deviceId())));
        // Only the server that stored a position analyzes it, so no event is produced twice
        var events = events(fixture.deviceId());
        assertEquals(events.size(), new HashSet<>(events).size());
    }

    @Test
    void concurrentLiveAndOfflineReportsOfOneDevice() throws Exception {
        var server = start();
        var fixture = seed(server, "mixed", Map.of());

        var ready = new CountDownLatch(2);
        ExecutorService executor = Executors.newFixedThreadPool(2);
        try {
            // Live points 15..26 race with an offline batch of 0..17; points 15..17 travel both ways
            Future<?> live = executor.submit(() -> {
                ready.countDown();
                ready.await();
                for (int k = 15; k < TOTAL; k++) {
                    int point = Math.min(k, MOVING - 1);
                    server.live("id=mixed&lat=" + routeLat(point) + "&lon=" + CENTER_LON + "&timestamp=" + time(k)
                            + "&speed=" + (k < MOVING ? 30 : 0));
                }
                return null;
            });
            Future<HttpResponse<String>> offline = executor.submit(() -> {
                ready.countDown();
                ready.await();
                return server.batch(batch(fixture, driveItems(0, 18)));
            });
            live.get();
            var response = offline.get();
            assertEquals(200, response.statusCode());
            assertEquals(18, body(response).get("accepted").asInt());
        } finally {
            executor.shutdown();
        }
        // Barrier: this batch is queued behind every earlier position of the device
        assertEquals(List.of("duplicate"), statuses(server.batch(batch(fixture, List.of(driveItem(TOTAL - 1))))));

        var stored = positions(fixture.deviceId());
        assertEquals(TOTAL, stored.size(), "each report stored once");
        assertEquals(time(TOTAL - 1), fixTime(latestPositionId(fixture.deviceId())));
    }

    @Test
    void crashAfterCommitBeforeAcknowledgementThenRetry() throws Exception {
        var crashed = start();
        var fixture = seed(crashed, "crash", Map.of());
        String body = batch(fixture, driveItems(0, TOTAL));

        var lost = crashed.batch(body); // stored and committed; the consumer never sees this reply
        assertEquals(200, lost.statusCode());
        int eventsBefore = events(fixture.deviceId()).size();
        assertTrue(eventsBefore > 0);
        crashed.close();
        instances.remove(crashed);

        var restarted = start();
        restarted.watch(fixture.userId());
        var retry = restarted.batch(body);
        assertEquals(200, retry.statusCode());
        assertEquals(TOTAL, body(retry).get("accepted").asInt());
        assertEquals(repeat("duplicate", TOTAL), statuses(retry));
        for (int i = 0; i < TOTAL; i++) {
            assertEquals(body(lost).at("/results/" + i + "/positionId").asLong(),
                    body(retry).at("/results/" + i + "/positionId").asLong());
        }
        assertEquals(TOTAL, positions(fixture.deviceId()).size());
        assertEquals(eventsBefore, events(fixture.deviceId()).size(), "no event is produced again");
        assertEquals(0, restarted.socketEvents.size(), "no notification is sent again");
        assertEquals(0, restarted.socketPositions.size());
    }

    @Test
    void partialFailureIsRetriedWithoutDuplicates() throws Exception {
        var server = start();
        var fixture = seed(server, "partial", Map.of());
        failInsertsAt(fixture.deviceId(), routeLat(5));

        String body = batch(fixture, driveItems(0, 10));
        var response = server.batch(body);
        assertEquals(503, response.statusCode());
        var expected = new ArrayList<>(repeat("stored", 10));
        expected.set(5, "failed");
        assertEquals(expected, statuses(response));
        assertEquals(5, body(response).get("accepted").asInt(), "only the prefix before the failure may be deleted");
        assertEquals(9, positions(fixture.deviceId()).size());
        assertEquals(time(9), fixTime(latestPositionId(fixture.deviceId())));

        repairInserts();
        var retry = server.batch(body);
        assertEquals(200, retry.statusCode());
        expected = new ArrayList<>(repeat("duplicate", 10));
        expected.set(5, "stored");
        assertEquals(expected, statuses(retry));
        assertEquals(10, positions(fixture.deviceId()).size());
        assertEquals(time(9), fixTime(latestPositionId(fixture.deviceId())), "the retried point is late now");
    }

    @Test
    void failedLiveInsertLeavesCurrentStateAlone() throws Exception {
        var server = startSynchronous();
        var fixture = seed(server, "broken", Map.of());
        server.live(liveQuery(fixture, 4, 30));
        long current = latestPositionId(fixture.deviceId());
        failInsertsAt(fixture.deviceId(), routeLat(5));

        server.live(liveQuery(fixture, 5, 30));

        assertEquals(1, positions(fixture.deviceId()).size());
        assertEquals(current, latestPositionId(fixture.deviceId()), "an unstored position is never current");
        assertEquals(0, events(fixture.deviceId()).stream().filter(e -> e.positionId() == 0).count());
    }

    @Test
    void anOlderPositionOnAnotherServerDoesNotReplaceTheCurrentOne() throws Exception {
        var first = startSynchronous();
        var second = startSynchronous();
        var fixture = seed(first, "servers", Map.of());
        second.watch(fixture.userId());

        // The second server sees an early position, then misses the newest one, which the first server stores
        second.live(liveQuery(fixture, 5, 0));
        first.live(liveQuery(fixture, 20, 0));
        long newest = latestPositionId(fixture.deviceId());
        second.live(liveQuery(fixture, 10, 0));

        assertEquals(newest, latestPositionId(fixture.deviceId()));
        assertEquals(3, positions(fixture.deviceId()).size());
        assertTrue(second.socketPositions.stream().noneMatch(p -> p.getFixTime().toInstant().equals(time(10))),
                "the stale position is not pushed as current");

        second.live(liveQuery(fixture, 21, 0));
        assertNotEquals(newest, latestPositionId(fixture.deviceId()));
        assertEquals(time(21), fixTime(latestPositionId(fixture.deviceId())));
    }

    @Test
    void shuffledTrafficAcrossTwoServersConvergesOnTheNewest() throws Exception {
        var first = start();
        var second = start();
        var fixture = seed(first, "converge", Map.of());

        var items = new ArrayList<>(driveItems(0, TOTAL));
        Collections.shuffle(items, new Random(11));
        var chunks = List.of(items.subList(0, 7), items.subList(7, 14), items.subList(14, 21), items.subList(21, 27));
        var ready = new CountDownLatch(chunks.size());
        ExecutorService executor = Executors.newFixedThreadPool(chunks.size());
        try {
            var futures = new ArrayList<Future<HttpResponse<String>>>();
            for (int i = 0; i < chunks.size(); i++) {
                var server = i % 2 == 0 ? first : second;
                String body = batch(fixture, chunks.get(i));
                futures.add(executor.submit(() -> {
                    ready.countDown();
                    ready.await();
                    return server.batch(body);
                }));
            }
            for (var future : futures) {
                assertEquals(200, future.get().statusCode());
            }
        } finally {
            executor.shutdown();
        }

        assertEquals(TOTAL, positions(fixture.deviceId()).size());
        assertEquals(time(TOTAL - 1), fixTime(latestPositionId(fixture.deviceId())));
    }

    @Test
    void oneDevicesFailuresDoNotAffectAnother() throws Exception {
        var server = start();
        var broken = seed(server, "broken-device", Map.of());
        var healthy = seed(server, "healthy-device", Map.of());
        failInsertsAt(broken.deviceId(), routeLat(10));

        var ready = new CountDownLatch(2);
        ExecutorService executor = Executors.newFixedThreadPool(2);
        try {
            var a = executor.submit(() -> {
                ready.countDown();
                ready.await();
                return server.batch(batch(broken, driveItems(0, 15)));
            });
            var b = executor.submit(() -> {
                ready.countDown();
                ready.await();
                return server.batch(batch(healthy, driveItems(0, 15)));
            });
            assertEquals(503, a.get().statusCode());
            assertEquals(200, b.get().statusCode());
            assertEquals(repeat("stored", 15), statuses(b.get()));
        } finally {
            executor.shutdown();
        }

        assertEquals(14, positions(broken.deviceId()).size());
        assertEquals(15, positions(healthy.deviceId()).size());
        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(11)),
                eventTrail(healthy.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(List.of(),
                eventTrail(broken.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT),
                "the only inside point of the broken device was not stored");
        assertEquals(time(14), fixTime(latestPositionId(healthy.deviceId())));
    }

    @Test
    void validationAndRetentionOutcomes() throws Exception {
        var server = start();
        var fixture = seed(server, "policy", Map.of());

        var response = server.batch(batch(fixture, List.of(
                batchItem(base.minus(Duration.ofDays(8)), routeLat(0), CENTER_LON, 0, null),
                batchItem(base.plus(Duration.ofDays(1)), routeLat(0), CENTER_LON, 0, null),
                "{\"timestamp\":\"nonsense\",\"lat\":1,\"lon\":2}",
                batchItem(time(1), 0, 0, 0, null),
                routeItem(2, 0))));
        assertEquals(200, response.statusCode(), "every item has a final outcome");
        assertEquals(5, body(response).get("accepted").asInt());
        assertEquals(List.of("quarantined", "quarantined", "rejected", "rejected", "stored"), statuses(response));
        assertEquals("too old", body(response).at("/results/0/reason").asText());
        assertEquals("future", body(response).at("/results/1/reason").asText());
        assertEquals("invalid", body(response).at("/results/2/reason").asText());
        assertEquals("filtered", body(response).at("/results/3/reason").asText());
        assertEquals(1, positions(fixture.deviceId()).size());

        assertEquals(422, server.batch("{\"deviceId\":\"nobody\",\"positions\":[" + routeItem(0, 0) + "]}")
                .statusCode());
        assertEquals(400, server.batch("{\"deviceId\":\"policy\",\"positions\":[]}").statusCode());
        assertEquals(400, server.batch("not json").statusCode());
    }

    @Test
    void unfinishedItemsTimeOutAsFailedAndAreSafeToResend() throws Exception {
        // Hold positions in the reordering buffer longer than the endpoint waits for them
        var slow = start(Map.of("server.buffering.threshold", "3000", "ingest.timeout", "1"));
        var fixture = seed(slow, "slow", Map.of());
        String body = batch(fixture, driveItems(0, 3));

        var first = slow.batch(body);
        assertEquals(503, first.statusCode());
        assertEquals(0, body(first).get("accepted").asInt());
        assertEquals(repeat("failed", 3), statuses(first));

        // "failed" is ambiguous: the positions are still stored once the buffer releases them
        long deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos();
        while (positions(fixture.deviceId()).size() < 3 && System.nanoTime() < deadline) {
            Thread.onSpinWait();
        }
        assertEquals(3, positions(fixture.deviceId()).size());

        var resend = start().batch(body);
        assertEquals(200, resend.statusCode());
        assertEquals(repeat("duplicate", 3), statuses(resend));
        assertEquals(3, positions(fixture.deviceId()).size());
    }

    @Test
    void latestPositionGuardIsAtomicUnderConcurrency() throws Exception {
        var server = start();
        var fixture = seed(server, "guard", Map.of());
        var storage = server.storage();

        var stored = new ArrayList<long[]>(); // id, fix time
        for (int k = 0; k < 40; k++) {
            var position = new org.traccar.model.Position("osmand");
            position.setDeviceId(fixture.deviceId());
            position.setTime(java.util.Date.from(time(k % 20))); // pairs of positions share a fix time
            position.setLatitude(routeLat(k % 20));
            position.setLongitude(CENTER_LON);
            long id = storage.addPosition(position, "guard-" + k).positionId();
            stored.add(new long[] {id, position.getFixTime().getTime()});
        }
        Collections.shuffle(stored, new Random(3));
        var ready = new CountDownLatch(8);
        ExecutorService executor = Executors.newFixedThreadPool(8);
        try {
            var futures = new ArrayList<Future<?>>();
            for (int t = 0; t < 8; t++) {
                int offset = t;
                futures.add(executor.submit(() -> {
                    ready.countDown();
                    ready.await();
                    for (int i = offset; i < stored.size(); i += 8) {
                        storage.updateLatestPosition(
                                fixture.deviceId(), stored.get(i)[0], new java.util.Date(stored.get(i)[1]));
                    }
                    return null;
                }));
            }
            for (var future : futures) {
                future.get();
            }
        } finally {
            executor.shutdown();
        }

        // Newest fix time wins; between equal fix times, the higher id
        long expected = stored.stream()
                .max(java.util.Comparator.<long[]>comparingLong(p -> p[1]).thenComparingLong(p -> p[0]))
                .orElseThrow()[0];
        assertEquals(expected, latestPositionId(fixture.deviceId()));
        var older = stored.stream().filter(p -> p[1] == time(0).toEpochMilli()).findFirst().orElseThrow();
        assertFalse(storage.updateLatestPosition(fixture.deviceId(), older[0], new java.util.Date(older[1])));
        assertEquals(expected, latestPositionId(fixture.deviceId()));
    }

    @Test
    void receiptsOutliveTheAcceptedAgeOnly() throws Exception {
        var server = start();
        var fixture = seed(server, "retention", Map.of());
        var response = server.batch(batch(fixture, List.of(routeItem(1, 0))));
        assertEquals("stored", body(response).at("/results/0/status").asText());
        try (var connection = connect(); var statement = connection.createStatement()) {
            statement.execute("INSERT INTO tc_position_receipts (deviceid, eventid, positionid, fixtime) VALUES ("
                    + fixture.deviceId() + ", 'ancient', 1, now() - interval '9 days')");
        }

        new org.traccar.schedule.TaskDeleteReceipts(new org.traccar.config.Config(), server.storage()).run();

        try (var connection = connect(); var statement = connection.createStatement();
             var rs = statement.executeQuery("SELECT eventid FROM tc_position_receipts ORDER BY eventid")) {
            var left = new ArrayList<String>();
            while (rs.next()) {
                left.add(rs.getString(1));
            }
            assertEquals(List.of(IngestTicketIds.fix(time(1), routeLat(1), CENTER_LON)), left);
        }
    }

}
