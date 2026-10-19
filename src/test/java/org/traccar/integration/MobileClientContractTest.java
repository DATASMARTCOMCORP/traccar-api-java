package org.traccar.integration;

import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import java.util.stream.IntStream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Checks the endpoint replies against how the mobile library reads them. The rules below are transcribed from
 * track-client-lib: the batch client on branch origin/feat/cpd-ordered-fix-delivery
 * (android/src/main/java/datasmartcom/modules/trackclient/QueueBatcher.kt, outcomeOf/decide/parseAccepted, and
 * SendOutcome.kt, fromHttpStatus) and the single-request client on main (TrackingService.kt: any 2xx is success).
 * If those files change, this transcription must change with them.
 */
class MobileClientContractTest extends IngestionTestSupport {

    // QueueBatcher.acceptedPattern
    private static final Pattern ACCEPTED = Pattern.compile("\"accepted\"\\s*:\\s*(\\d+)", Pattern.CASE_INSENSITIVE);

    private sealed interface Outcome permits Accepted, Retry, ClientError, EndpointMissing { }
    private record Accepted(int count) implements Outcome { }
    private record Retry() implements Outcome { }
    private record ClientError(int status) implements Outcome { }
    private record EndpointMissing() implements Outcome { }

    // QueueBatcher.outcomeOf with SendOutcome.fromHttpStatus
    private static Outcome outcomeOf(int status, String body) {
        if (status == 404 || status == 405) {
            return new EndpointMissing();
        }
        if (status >= 200 && status <= 299) {
            var matcher = ACCEPTED.matcher(body);
            return matcher.find() ? new Accepted(Integer.parseInt(matcher.group(1))) : new Retry();
        }
        if (status == 408 || status == 429 || status >= 500) {
            return new Retry();
        }
        return new ClientError(status);
    }

    /** The client's queue: delete the accepted prefix, keep everything on retry. */
    private int drainOnce(TraccarInstance server, Fixture fixture, List<String> queue) throws Exception {
        var response = server.batch(batch(fixture, queue));
        var outcome = outcomeOf(response.statusCode(), response.body());
        if (outcome instanceof Accepted accepted) {
            int n = Math.max(0, Math.min(accepted.count(), queue.size()));
            queue.subList(0, n).clear();
            return n;
        }
        assertTrue(outcome instanceof Retry, "unexpected outcome " + outcome);
        return 0;
    }

    @Test
    void batchClientRetriesUntilEverythingIsStoredExactlyOnce() throws Exception {
        var server = start();
        var fixture = seed(server, "phone", Map.of());
        var queue = new ArrayList<>(IntStream.range(0, 30)
                .mapToObj(k -> batchItem(time(k), routeLat(k % 20), CENTER_LON + k * 1e-5, 10, null)).toList());
        try (var connection = connect(); var statement = connection.createStatement()) {
            statement.execute("CREATE OR REPLACE FUNCTION injected_failure() RETURNS trigger AS $$ BEGIN "
                    + "RAISE EXCEPTION 'injected failure'; END $$ LANGUAGE plpgsql");
            statement.execute("CREATE TRIGGER injected_failure BEFORE INSERT ON tc_positions FOR EACH ROW WHEN "
                    + "(abs(NEW.longitude - (" + (CENTER_LON + 12 * 1e-5) + ")) < 1e-9) "
                    + "EXECUTE FUNCTION injected_failure()");
        }

        // A failed item makes the reply a 503: the client keeps the whole block, although most of it is stored
        assertEquals(0, drainOnce(server, fixture, queue));
        assertEquals(30, queue.size());
        assertEquals(29, positions(fixture.deviceId()).size());

        try (var connection = connect(); var statement = connection.createStatement()) {
            statement.execute("DROP TRIGGER injected_failure ON tc_positions");
        }
        assertEquals(30, drainOnce(server, fixture, queue));
        assertTrue(queue.isEmpty());
        assertEquals(30, positions(fixture.deviceId()).size(), "resent items are recognized, not stored again");
    }

    @Test
    void itemsTheServerWillNeverStoreDoNotBlockTheQueue() throws Exception {
        var server = start();
        var fixture = seed(server, "phone", Map.of());
        var queue = new ArrayList<>(List.of(
                batchItem(base.minus(Duration.ofDays(30)), routeLat(0), CENTER_LON, 0, null),
                "{\"timestamp\":\"garbage\",\"lat\":1,\"lon\":1}",
                routeItem(1, 0)));

        assertEquals(3, drainOnce(server, fixture, queue));
        assertTrue(queue.isEmpty());
        assertEquals(1, positions(fixture.deviceId()).size());
    }

    @Test
    void unknownDeviceIsAClientError() throws Exception {
        var server = start();
        var response = server.batch("{\"deviceId\":\"unregistered\",\"positions\":[" + routeItem(0, 0) + "]}");
        assertEquals(new ClientError(422), outcomeOf(response.statusCode(), response.body()));
    }

    @Test
    void singleRequestClientResendingAfterATimeoutStoresOnce() throws Exception {
        var server = startSynchronous();
        var fixture = seed(server, "phone", Map.of());
        // main's TrackingService counts any 2xx as delivered; a resend after a lost reply must not duplicate
        assertEquals(200, server.live(liveQuery(fixture, 3, 10)).statusCode());
        assertEquals(200, server.live(liveQuery(fixture, 3, 10)).statusCode());
        assertEquals(1, positions(fixture.deviceId()).size());
    }

}
