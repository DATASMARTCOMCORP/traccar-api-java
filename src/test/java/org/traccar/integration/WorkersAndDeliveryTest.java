package org.traccar.integration;

import com.fasterxml.jackson.databind.JsonNode;
import org.junit.jupiter.api.Test;
import org.traccar.database.OutboxStore;
import org.traccar.schedule.TaskOutbox;

import java.net.http.HttpResponse;
import java.sql.Connection;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;
import java.util.stream.Collectors;
import java.util.stream.IntStream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Processing workers (lock waits off the network threads, bounded backlog) and outbox delivery when several
 * servers drain it: atomic claims, crashed claimers, retry backoff and leases expiring mid-delivery.
 */
class WorkersAndDeliveryTest extends IngestionTestSupport {

    private List<String> items(int from, int to) {
        return IntStream.range(from, to).mapToObj(k -> routeItem(k, 30)).toList();
    }

    private static List<String> statuses(HttpResponse<String> response) throws Exception {
        var result = new ArrayList<String>();
        for (JsonNode item : body(response).get("results")) {
            result.add(item.get("status").asText() + (item.has("reason") ? ":" + item.get("reason").asText() : ""));
        }
        return result;
    }

    private long count(String query) {
        try (var connection = connect(); var statement = connection.createStatement();
             var rs = statement.executeQuery(query)) {
            rs.next();
            return rs.getLong(1);
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    private void sql(String statement) throws Exception {
        try (var connection = connect(); var jdbc = connection.createStatement()) {
            jdbc.execute(statement);
        }
    }

    private Connection holdLock(long deviceId) throws Exception {
        Connection owner = connect();
        owner.setAutoCommit(false);
        owner.createStatement().execute("SELECT pg_advisory_xact_lock(" + 0x7472 + ", " + deviceId + ")");
        return owner;
    }

    private void awaitLockWaiters() {
        await(() -> count("SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted") > 0,
                "a server waiting for the device lock");
    }

    private static CompletableFuture<HttpResponse<String>> async(Callable call) {
        return CompletableFuture.supplyAsync(() -> {
            try {
                return call.run();
            } catch (Exception e) {
                throw new RuntimeException(e);
            }
        });
    }

    private interface Callable {
        HttpResponse<String> run() throws Exception;
    }

    @Test
    void aDeviceWaitingForItsLockDoesNotStallOtherDevices() throws Exception {
        // One network thread: before the workers, a lock wait on it blocked every connection
        var server = start(Map.of("server.nettyThreads", "1", "processing.lockTimeout", "20000"));
        var waiting = seed(server, "waiting", Map.of());
        var other = seed(server, "other", Map.of());

        Connection owner = holdLock(waiting.deviceId());
        var blocked = async(() -> server.batch(batch(waiting, items(0, 3))));
        awaitLockWaiters();

        var response = async(() -> server.batch(batch(other, items(0, 3)))).get(10, TimeUnit.SECONDS);
        assertEquals(List.of("stored", "stored", "stored"), statuses(response));
        assertFalse(blocked.isDone(), "the first device is still waiting for its lock");

        owner.close();
        assertEquals(List.of("stored", "stored", "stored"), statuses(blocked.get(10, TimeUnit.SECONDS)));
    }

    @Test
    void aFullWorkerRefusesPositionsInsteadOfQueueingWithoutBound() throws Exception {
        var server = start(Map.of(
                "processing.workers", "1", "processing.queueLimit", "2", "processing.lockTimeout", "20000"));
        var fixture = seed(server, "backlog", Map.of());

        Connection owner = holdLock(fixture.deviceId());
        var response = async(() -> server.batch(batch(fixture, items(0, 10))));
        awaitLockWaiters();
        owner.close();

        var result = response.get(10, TimeUnit.SECONDS);
        assertEquals(503, result.statusCode());
        var expected = new ArrayList<>(Collections.nCopies(4, "stored"));
        expected.addAll(Collections.nCopies(6, "failed:busy"));
        assertEquals(expected, statuses(result), "twice the limit is admitted, in fix time order");
        assertEquals(4, body(result).get("accepted").asInt());
        assertEquals(4, positions(fixture.deviceId()).size());

        // The refused ones are retried, in batches the worker can take (a whole batch is admitted at once)
        assertEquals(200, server.batch(batch(fixture, items(4, 8))).statusCode());
        assertEquals(200, server.batch(batch(fixture, items(8, 10))).statusCode());
        assertEquals(10, positions(fixture.deviceId()).size());
    }

    /** Leaves every event delivery undelivered and claimable, as after failed attempts and a restart. */
    private long pendingDeliveries(TraccarInstance server, Fixture fixture) throws Exception {
        webhookStatus = 500;
        assertEquals(200, server.batch(batch(fixture, items(0, 21))).statusCode());
        long events = events(fixture.deviceId()).size();
        assertTrue(events >= 2);
        await(() -> count("SELECT count(*) FROM tc_outbox WHERE kind = 'event' AND attempts > 0") == events,
                "first delivery attempts");
        sql("UPDATE tc_outbox SET claimeduntil = NULL WHERE deliveredat IS NULL");
        webhookStatus = 200;
        return events;
    }

    private Map<Long, Long> forwardedPerEvent() {
        return forwarded.stream().collect(Collectors.groupingBy(f -> f.at("/event/id").asLong(),
                Collectors.counting()));
    }

    @Test
    void serversDrainingTogetherDeliverEachEntryOnce() throws Exception {
        var servers = List.of(start(), start(), start());
        var fixture = seed(servers.get(0), "drain", Map.of());
        long events = pendingDeliveries(servers.get(0), fixture);

        var ready = new CountDownLatch(servers.size());
        ExecutorService executor = Executors.newFixedThreadPool(servers.size());
        try {
            var runs = new ArrayList<Future<?>>();
            for (var server : servers) {
                runs.add(executor.submit(() -> {
                    ready.countDown();
                    ready.await();
                    server.get(TaskOutbox.class).run();
                    return null;
                }));
            }
            for (var run : runs) {
                run.get();
            }
        } finally {
            executor.shutdown();
        }
        awaitForwarded((int) events);
        await(() -> count("SELECT count(*) FROM tc_outbox WHERE deliveredat IS NULL") == 0, "all delivered");

        assertEquals(events, forwardedPerEvent().size());
        assertTrue(forwardedPerEvent().values().stream().allMatch(n -> n == 1), forwardedPerEvent().toString());
    }

    @Test
    void entriesClaimedByACrashedWorkerWaitForTheClaimToExpire() throws Exception {
        var server = start();
        var fixture = seed(server, "claimed", Map.of());
        long events = pendingDeliveries(server, fixture);

        // A worker claimed everything and died
        sql("UPDATE tc_outbox SET claimeduntil = now() + interval '1 hour' WHERE deliveredat IS NULL");
        server.get(TaskOutbox.class).run();
        assertEquals(events, count("SELECT count(*) FROM tc_outbox WHERE kind = 'event' AND deliveredat IS NULL"));

        // Its claims expire
        sql("UPDATE tc_outbox SET claimeduntil = now() - interval '1 second' WHERE deliveredat IS NULL");
        server.get(TaskOutbox.class).run();
        awaitForwarded((int) events);
        await(() -> count("SELECT count(*) FROM tc_outbox WHERE deliveredat IS NULL") == 0, "all delivered");
        assertTrue(forwardedPerEvent().values().stream().allMatch(n -> n == 1));
    }

    @Test
    void concurrentClaimsOfOneEntryHaveOneWinner() throws Exception {
        var server = start();
        var outbox = server.get(OutboxStore.class);
        var fixture = seed(server, "claims", Map.of());
        for (int round = 0; round < 20; round++) {
            var entry = outbox.add(OutboxStore.EVENT, fixture.deviceId(), 1000 + round);
            var ready = new CountDownLatch(8);
            ExecutorService executor = Executors.newFixedThreadPool(8);
            try {
                var claims = new ArrayList<Future<Boolean>>();
                for (int i = 0; i < 8; i++) {
                    claims.add(executor.submit(() -> {
                        ready.countDown();
                        ready.await();
                        return outbox.claim(entry);
                    }));
                }
                int winners = 0;
                for (var claim : claims) {
                    winners += claim.get() ? 1 : 0;
                }
                assertEquals(1, winners, "round " + round);
            } finally {
                executor.shutdown();
            }
        }
    }

    @Test
    void failedDeliveriesBackOffExponentiallyUpToTenMinutes() throws Exception {
        var server = start();
        var outbox = server.get(OutboxStore.class);
        var fixture = seed(server, "backoff", Map.of());
        outbox.add(OutboxStore.EVENT, fixture.deviceId(), 123456);

        Function<Void, Long> claimDelay = ignore -> count(
                "SELECT round(extract(epoch FROM claimeduntil - now())) FROM tc_outbox WHERE objectid = 123456");
        var delays = new ArrayList<Long>();
        for (int attempt = 0; attempt < 9; attempt++) {
            var entry = outbox.pending(100).stream().filter(e -> e.objectId() == 123456).findFirst().orElseThrow();
            assertEquals(attempt, entry.attempts());
            assertTrue(outbox.claim(entry));
            assertFalse(outbox.claim(entry), "a claimed entry can't be claimed again");
            outbox.retryLater(entry);
            delays.add(claimDelay.apply(null));
            assertTrue(outbox.pending(100).stream().noneMatch(e -> e.objectId() == 123456), "not before its time");
            sql("UPDATE tc_outbox SET claimeduntil = now() - interval '1 second' WHERE objectid = 123456");
        }
        assertEquals(List.of(5L, 10L, 20L, 40L, 80L, 160L, 320L, 600L, 600L), delays);
    }

    @Test
    void aLeaseExpiringMidDeliveryRepeatsItWithTheSameEventId() throws Exception {
        holdFirstWebhook = new CountDownLatch(1);
        // The receiver is slow, not dead: the HTTP call outlives the lease instead of timing out
        var slow = start(Map.of("outbox.lease", "500", "client.readTimeout", "60000"));
        var fixture = seed(slow, "slow-receiver", Map.of());
        // Point 10 alone: one geofence enter, delivered from the fast path and stuck in the receiver
        assertEquals(200, slow.batch(batch(fixture, items(10, 11))).statusCode());
        assertEquals(1, events(fixture.deviceId()).size());
        long eventId = count("SELECT min(objectid) FROM tc_outbox WHERE kind = 'event'");
        await(() -> count("SELECT count(*) FROM tc_outbox WHERE kind = 'event' AND claimeduntil < now()") == 1,
                "the lease of the stuck delivery to expire");

        var other = start();
        other.get(TaskOutbox.class).run();
        awaitForwarded(1); // the second attempt
        holdFirstWebhook.countDown();
        awaitForwarded(2); // the first one finally answers

        assertEquals(List.of(eventId, eventId), forwarded.stream().map(f -> f.at("/event/id").asLong()).toList());
        await(() -> count("SELECT count(*) FROM tc_outbox WHERE deliveredat IS NULL") == 0, "marked delivered");
    }

}
