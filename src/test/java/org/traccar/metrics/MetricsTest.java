package org.traccar.metrics;

import org.junit.jupiter.api.Test;
import org.traccar.config.Config;
import org.traccar.config.Keys;

import java.io.IOException;
import java.net.ServerSocket;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

public class MetricsTest {

    private static int freePort() throws IOException {
        try (var socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static String scrape(int port) throws Exception {
        var request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/metrics")).build();
        return HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString()).body();
    }

    @Test
    public void testDisabledByDefault() {
        var metrics = new Metrics(new Config());
        assertFalse(metrics.isEnabled());
        assertDoesNotThrow(() -> {
            metrics.messageReceived("osmand");
            metrics.positionProcessed("stored", System.nanoTime());
            metrics.gauge("traccar.sessions", "{session}", "", () -> 1);
            metrics.stop();
        });
    }

    @Test
    public void testPrometheusEndpoint() throws Exception {
        int port = freePort();
        var config = new Config();
        config.setString(Keys.METRICS_ENABLE, "true");
        config.setString(Keys.METRICS_PORT, String.valueOf(port));
        config.setString(Keys.METRICS_ENVIRONMENT, "test");
        var metrics = new Metrics(config);
        try {
            assertTrue(metrics.isEnabled());
            metrics.gauge("traccar.sessions", "{session}", "Sessions", () -> 3);
            metrics.messageReceived("osmand");
            metrics.positionsDecoded("osmand", 2);
            metrics.decodingError("suntech");
            metrics.lockAcquired(System.nanoTime());
            metrics.transactionFinished("committed", 1_000_000);
            metrics.positionProcessed("stored", System.nanoTime());
            metrics.positionSkipped("refused");
            metrics.forwarded("event", System.nanoTime(), false);

            String body = scrape(port);
            for (String expected : new String[] {
                    "traccar_sessions{", "traccar_messages_received_total{", "protocol=\"osmand\"",
                    "traccar_positions_decoded_total{", "traccar_decoding_errors_total{",
                    "traccar_db_lock_duration_seconds_bucket{", "traccar_db_commit_duration_seconds_count{",
                    "traccar_db_transactions_total{", "traccar_positions_processed_total{",
                    "traccar_processing_duration_seconds_sum{", "traccar_forward_requests_total{",
                    "result=\"failure\"", "jvm_memory_used_bytes{", "jvm_gc_collections_total{",
                    "jvm_thread_count{", "deployment_environment=\"test\"", "service_name=\"traccar\""}) {
                assertTrue(body.contains(expected), expected + " missing from:\n" + body);
            }
        } finally {
            metrics.stop();
        }
    }

    @Test
    public void testPortInUseKeepsServerRunning() throws Exception {
        try (var socket = new ServerSocket(0, 50, java.net.InetAddress.getByName("127.0.0.1"))) {
            var config = new Config();
            config.setString(Keys.METRICS_ENABLE, "true");
            config.setString(Keys.METRICS_PORT, String.valueOf(socket.getLocalPort()));
            var metrics = new Metrics(config);
            assertFalse(metrics.isEnabled());
            assertDoesNotThrow(() -> metrics.messageReceived("osmand"));
        }
    }

}
