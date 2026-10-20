package org.traccar.sqs;

import org.junit.jupiter.api.Test;
import org.traccar.ProcessingHandler;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.database.DeviceLookupService;
import org.traccar.database.IngestOutcome;
import org.traccar.model.Device;
import org.traccar.model.Position;
import org.traccar.session.ConnectionManager;
import software.amazon.awssdk.services.sqs.SqsAsyncClient;
import software.amazon.awssdk.services.sqs.model.BatchResultErrorEntry;
import software.amazon.awssdk.services.sqs.model.ChangeMessageVisibilityBatchRequest;
import software.amazon.awssdk.services.sqs.model.ChangeMessageVisibilityBatchResponse;
import software.amazon.awssdk.services.sqs.model.DeleteMessageBatchRequest;
import software.amazon.awssdk.services.sqs.model.DeleteMessageBatchResponse;
import software.amazon.awssdk.services.sqs.model.Message;
import software.amazon.awssdk.services.sqs.model.MessageSystemAttributeName;
import software.amazon.awssdk.services.sqs.model.SendMessageBatchRequest;
import software.amazon.awssdk.services.sqs.model.SendMessageBatchResponse;
import software.amazon.awssdk.services.sqs.model.SendMessageBatchResultEntry;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

class SqsPositionConsumerTest {

    private static final String QUEUE = "https://sqs.test/queue";
    private static final String DEAD_LETTER_QUEUE = "https://sqs.test/dead";

    /** Records the requests and answers them; the consumer-builder overloads of the client delegate here. */
    private static final class FakeSqs implements SqsAsyncClient {

        final List<DeleteMessageBatchRequest> deletes = new ArrayList<>();
        final List<ChangeMessageVisibilityBatchRequest> visibilityChanges = new ArrayList<>();
        final List<SendMessageBatchRequest> sends = new ArrayList<>();
        boolean failSends;

        @Override
        public CompletableFuture<DeleteMessageBatchResponse> deleteMessageBatch(DeleteMessageBatchRequest request) {
            deletes.add(request);
            return CompletableFuture.completedFuture(DeleteMessageBatchResponse.builder().build());
        }

        @Override
        public CompletableFuture<ChangeMessageVisibilityBatchResponse> changeMessageVisibilityBatch(
                ChangeMessageVisibilityBatchRequest request) {
            visibilityChanges.add(request);
            return CompletableFuture.completedFuture(ChangeMessageVisibilityBatchResponse.builder().build());
        }

        @Override
        public CompletableFuture<SendMessageBatchResponse> sendMessageBatch(SendMessageBatchRequest request) {
            sends.add(request);
            var response = SendMessageBatchResponse.builder();
            if (failSends) {
                response.failed(request.entries().stream()
                        .map(entry -> BatchResultErrorEntry.builder().id(entry.id()).code("Error").build())
                        .toList());
            } else {
                response.successful(request.entries().stream()
                        .map(entry -> SendMessageBatchResultEntry.builder().id(entry.id()).build())
                        .toList());
            }
            return CompletableFuture.completedFuture(response.build());
        }

        @Override
        public String serviceName() {
            return "sqs";
        }

        @Override
        public void close() {
        }
    }

    private final FakeSqs sqs = new FakeSqs();
    private final ProcessingHandler processingHandler = mock(ProcessingHandler.class);
    private final ConnectionManager connectionManager = mock(ConnectionManager.class);
    private final List<Position> processed = new ArrayList<>();

    private SqsPositionConsumer consumer(boolean deadLetterQueue, Function<Position, IngestOutcome> outcome) {
        var config = new Config();
        config.setString(Keys.SQS_QUEUE_URL, QUEUE);
        if (deadLetterQueue) {
            config.setString(Keys.SQS_DEAD_LETTER_QUEUE_URL, DEAD_LETTER_QUEUE);
        }
        var device = new Device();
        device.setId(7);
        device.setUniqueId("phone");
        var deviceLookupService = mock(DeviceLookupService.class);
        when(deviceLookupService.lookup(any())).thenAnswer(invocation ->
                ((String[]) invocation.getArgument(0))[0].equals("phone") ? device : null);
        doAnswer(invocation -> {
            Position position = invocation.getArgument(0);
            processed.add(position);
            position.getIngestTicket().complete(outcome.apply(position));
            return null;
        }).when(processingHandler).process(any());
        return new SqsPositionConsumer(config, sqs, processingHandler, deviceLookupService, connectionManager);
    }

    private static Message message(String id, String body) {
        return Message.builder()
                .messageId(id)
                .receiptHandle("receipt-" + id)
                .body(body)
                .attributes(Map.of(MessageSystemAttributeName.APPROXIMATE_RECEIVE_COUNT, "3"))
                .build();
    }

    private static String batch(String deviceId, String positions) {
        return "{\"deviceId\":\"" + deviceId + "\",\"positions\":[" + positions + "]}";
    }

    private static void await(CompletableFuture<Void> future) throws Exception {
        future.get(5, TimeUnit.SECONDS);
    }

    @Test
    void deletesMessagesWhosePositionsAreFinal() throws Exception {
        var consumer = consumer(true, position -> IngestOutcome.stored(1));
        await(consumer.handle(List.of(message("a", batch("phone", """
                {"id":"e2","timestamp":1700000010,"lat":-22,"lon":-43},
                {"id":"e1","timestamp":1700000000,"lat":-22,"lon":-43},
                {"id":"e1","timestamp":1700000000,"lat":-22,"lon":-43}
                """)))));

        assertEquals(2, processed.size());
        assertEquals("e1", processed.get(0).getIngestTicket().getEventId());
        assertEquals(7, processed.get(0).getDeviceId());
        assertEquals("osmand", processed.get(0).getProtocol());
        assertEquals(1, sqs.deletes.size());
        assertEquals("receipt-a", sqs.deletes.getFirst().entries().getFirst().receiptHandle());
        assertTrue(sqs.visibilityChanges.isEmpty());
        assertTrue(sqs.sends.isEmpty());
        verify(connectionManager).updateDevice(eq(7L), eq(Device.STATUS_ONLINE), any());
    }

    @Test
    void retriesMessagesWithAnUnfinishedPosition() throws Exception {
        var consumer = consumer(true, position -> position.getIngestTicket().getEventId().equals("e2")
                ? IngestOutcome.failed("storage") : IngestOutcome.duplicate(1));
        await(consumer.handle(List.of(
                message("a", batch("phone", """
                        {"id":"e1","timestamp":1700000000,"lat":-22,"lon":-43},
                        {"id":"e2","timestamp":1700000010,"lat":-22,"lon":-43}
                        """)),
                message("b", batch("phone", "{\"id\":\"e3\",\"timestamp\":1700000020,\"lat\":-22,\"lon\":-43}")))));

        assertEquals(1, sqs.visibilityChanges.size());
        var change = sqs.visibilityChanges.getFirst().entries().getFirst();
        assertEquals("receipt-a", change.receiptHandle());
        assertEquals(120, change.visibilityTimeout());
        assertEquals(1, sqs.deletes.size());
        assertEquals("receipt-b", sqs.deletes.getFirst().entries().getFirst().receiptHandle());
    }

    @Test
    void deadLettersMessagesThatCanNeverPass() throws Exception {
        var consumer = consumer(true, position -> IngestOutcome.stored(1));
        await(consumer.handle(List.of(
                message("a", "not json"),
                message("b", batch("unknown", "{\"id\":\"e1\",\"timestamp\":1700000000,\"lat\":-22,\"lon\":-43}")),
                message("c", batch("phone", """
                        {"id":"e2","timestamp":1700000000,"lat":-22,"lon":-43},
                        {"id":"e3","timestamp":1700000000,"lat":95,"lon":-43}
                        """)))));

        assertEquals(1, processed.size());
        assertEquals(1, sqs.sends.size());
        var entries = sqs.sends.getFirst().entries();
        assertEquals(DEAD_LETTER_QUEUE, sqs.sends.getFirst().queueUrl());
        assertEquals(List.of("invalid body", "unknown device", "item 1: invalid"), entries.stream()
                .map(entry -> entry.messageAttributes().get(SqsPositionConsumer.REASON_ATTRIBUTE).stringValue())
                .toList());
        assertEquals(List.of("receipt-a", "receipt-b", "receipt-c"), sqs.deletes.getFirst().entries().stream()
                .map(entry -> entry.receiptHandle()).sorted().toList());
    }

    @Test
    void keepsDeadMessagesThatWereNotCopied() throws Exception {
        sqs.failSends = true;
        await(consumer(true, position -> IngestOutcome.stored(1)).handle(List.of(message("a", "not json"))));
        assertTrue(sqs.deletes.isEmpty());

        sqs.sends.clear();
        await(consumer(false, position -> IngestOutcome.stored(1)).handle(List.of(message("a", "not json"))));
        assertTrue(sqs.sends.isEmpty());
        assertTrue(sqs.deletes.isEmpty());
        verify(processingHandler, never()).process(any());
        verify(connectionManager, never()).updateDevice(anyLong(), any(), any());
    }

    @Test
    void backsOffLongerOnEachReceive() {
        assertEquals(30, SqsPositionConsumer.retryDelay(1));
        assertEquals(60, SqsPositionConsumer.retryDelay(2));
        assertEquals(480, SqsPositionConsumer.retryDelay(5));
        assertEquals(900, SqsPositionConsumer.retryDelay(6));
        assertEquals(900, SqsPositionConsumer.retryDelay(40));
    }

}
