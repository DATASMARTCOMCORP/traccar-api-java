package org.traccar.sqs;

import io.netty.buffer.Unpooled;
import io.netty.channel.embedded.EmbeddedChannel;
import io.netty.handler.codec.http.DefaultFullHttpRequest;
import io.netty.handler.codec.http.FullHttpResponse;
import io.netty.handler.codec.http.HttpMethod;
import io.netty.handler.codec.http.HttpResponseStatus;
import io.netty.handler.codec.http.HttpVersion;
import io.netty.handler.codec.http.QueryStringDecoder;
import jakarta.json.Json;
import jakarta.json.JsonObject;
import org.junit.jupiter.api.Test;
import org.traccar.protocol.OsmAndPositionBatch;
import software.amazon.awssdk.services.sqs.SqsAsyncClient;
import software.amazon.awssdk.services.sqs.model.SendMessageRequest;
import software.amazon.awssdk.services.sqs.model.SendMessageResponse;

import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class SqsPositionPublisherTest {

    /** Holds each publish open until the test completes it, as a slow SQS would. */
    private static final class PendingSqs implements SqsAsyncClient {

        final List<SendMessageRequest> requests = new ArrayList<>();
        final List<CompletableFuture<SendMessageResponse>> pending = new ArrayList<>();

        @Override
        public CompletableFuture<SendMessageResponse> sendMessage(SendMessageRequest request) {
            requests.add(request);
            var future = new CompletableFuture<SendMessageResponse>();
            pending.add(future);
            return future;
        }

        @Override
        public String serviceName() {
            return "sqs";
        }

        @Override
        public void close() {
        }
    }

    private final PendingSqs sqs = new PendingSqs();
    private final EmbeddedChannel channel = new EmbeddedChannel(new SqsPositionPublisher(sqs, "queue"));

    private void post(String uri, String body) {
        channel.writeInbound(new DefaultFullHttpRequest(HttpVersion.HTTP_1_1, HttpMethod.POST, uri,
                Unpooled.copiedBuffer(body, StandardCharsets.UTF_8)));
    }

    private FullHttpResponse response() {
        channel.runPendingTasks();
        return channel.readOutbound();
    }

    private static JsonObject json(String text) {
        try (var reader = Json.createReader(new StringReader(text))) {
            return reader.readObject();
        }
    }

    private static JsonObject fromForm(String query) {
        return SqsPositionPublisher.fromForm(new QueryStringDecoder("?" + query).parameters(), 1_700_000_000_000L);
    }

    @Test
    void convertsFormReportsToTheBatchFormat() {
        var message = fromForm("id=phone&lat=-22.5&lon=-43.1&timestamp=2026-10-20T10:00:00Z&speed=12&bearing=90"
                + "&altitude=760&accuracy=8&batt=80&charge=true&valid=true&reportId=r1&os=android");
        assertEquals("phone", message.getString("deviceId"));
        assertEquals("android", message.getString("os"));
        var item = message.getJsonArray("positions").getJsonObject(0);
        assertEquals("r1", item.getString("id"));
        assertEquals(90, item.getJsonNumber("heading").doubleValue());
        assertTrue(item.getBoolean("charge"));

        var position = OsmAndPositionBatch.parse(message.toString()).positions().getFirst();
        assertEquals(Instant.parse("2026-10-20T10:00:00Z"), position.getFixTime().toInstant());
        assertEquals(-22.5, position.getLatitude());
        assertEquals(12, position.getSpeed());
        assertEquals("r1", position.getIngestTicket().getEventId());
    }

    @Test
    void fillsTheTimeAndAcceptsLocationAndEpochs() {
        var item = fromForm("deviceid=phone&location=-22.5,-43.1").getJsonArray("positions").getJsonObject(0);
        assertEquals(1_700_000_000_000L, item.getJsonNumber("timestamp").longValue());
        assertEquals(-43.1, item.getJsonNumber("lon").doubleValue());
        assertEquals(1_700_000_000L, fromForm("id=phone&lat=1&lon=2&timestamp=1700000000")
                .getJsonArray("positions").getJsonObject(0).getJsonNumber("timestamp").longValue());
    }

    @Test
    void refusesFormReportsThatCouldNeverBeStored() {
        assertThrows(IllegalArgumentException.class, () -> fromForm("lat=1&lon=2"));
        assertThrows(RuntimeException.class, () -> fromForm("id=phone&lat=1"));
        assertThrows(RuntimeException.class, () -> fromForm("id=phone&lat=91&lon=2"));
        assertThrows(RuntimeException.class, () -> fromForm("id=phone&lat=1&lon=2&speed=fast"));
    }

    @Test
    void answersOnlyAfterTheQueueAccepted() {
        post("/?id=phone&lat=1&lon=2&timestamp=1700000000", "");
        assertEquals(1, sqs.requests.size());
        assertEquals("queue", sqs.requests.getFirst().queueUrl());
        assertNull(response());

        sqs.pending.getFirst().complete(SendMessageResponse.builder().build());
        assertEquals(HttpResponseStatus.OK, response().status());
    }

    @Test
    void answersRetriableWhenTheQueueRefuses() {
        post("/", "id=phone&lat=1&lon=2");
        sqs.pending.getFirst().completeExceptionally(new RuntimeException("down"));
        assertEquals(HttpResponseStatus.SERVICE_UNAVAILABLE, response().status());

        post("/", "id=phone&lat=1");
        assertEquals(HttpResponseStatus.BAD_REQUEST, response().status());
        assertEquals(1, sqs.requests.size());
    }

    @Test
    void publishesTheValidItemsOfABatch() {
        post(SqsPositionPublisher.BATCH_PATH, """
                {"deviceId":"phone","os":"ios","positions":[
                  {"id":"a","timestamp":1700000000,"lat":-22,"lon":-43},
                  {"id":"b","timestamp":1700000000,"lat":95,"lon":-43},
                  {"id":"a","timestamp":1700000000,"lat":-22,"lon":-43}
                ]}
                """);
        var message = json(sqs.requests.getFirst().messageBody());
        assertEquals("ios", message.getString("os"));
        assertEquals(1, message.getJsonArray("positions").size());
        assertNull(response());

        sqs.pending.getFirst().complete(SendMessageResponse.builder().build());
        var response = response();
        assertEquals(HttpResponseStatus.OK, response.status());
        var body = json(response.content().toString(StandardCharsets.UTF_8));
        assertEquals(3, body.getInt("accepted"));
        assertEquals(List.of("queued", "rejected", "queued"), body.getJsonArray("results").stream()
                .map(result -> result.asJsonObject().getString("status")).toList());
    }

    @Test
    void answersInvalidBatchesWithoutPublishing() {
        post(SqsPositionPublisher.BATCH_PATH, "{\"deviceId\":\"phone\",\"positions\":[]}");
        assertEquals(HttpResponseStatus.BAD_REQUEST, response().status());

        post(SqsPositionPublisher.BATCH_PATH, """
                {"deviceId":"phone","positions":[{"timestamp":1700000000,"lat":95,"lon":-43}]}
                """);
        var response = response();
        assertEquals(HttpResponseStatus.OK, response.status());
        assertTrue(sqs.requests.isEmpty());
    }

}
