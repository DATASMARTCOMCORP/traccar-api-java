package org.traccar.sqs;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.LifecycleObject;
import org.traccar.ProcessingHandler;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.database.DeviceLookupService;
import org.traccar.database.IngestOutcome;
import org.traccar.model.Device;
import org.traccar.model.Position;
import org.traccar.protocol.OsmAndPositionBatch;
import software.amazon.awssdk.services.sqs.SqsAsyncClient;
import software.amazon.awssdk.services.sqs.model.ChangeMessageVisibilityBatchRequestEntry;
import software.amazon.awssdk.services.sqs.model.DeleteMessageBatchRequestEntry;
import software.amazon.awssdk.services.sqs.model.Message;
import software.amazon.awssdk.services.sqs.model.MessageAttributeValue;
import software.amazon.awssdk.services.sqs.model.MessageSystemAttributeName;
import software.amazon.awssdk.services.sqs.model.ReceiveMessageResponse;
import software.amazon.awssdk.services.sqs.model.SendMessageBatchRequestEntry;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Consumes positions that an ingestion endpoint published to SQS, in the OsmAnd batch format, and runs them
 * through the same processing as positions from devices. A message is deleted only when every position in it has
 * a final outcome; until then it is retried, which is safe because stored positions are recognized by their id.
 *
 * <p>Nothing waits on a thread: each receive loop is a chain of futures. The futures of the SDK complete on its
 * own threads and the outcomes on the processing workers, so the work between them (parsing, device lookup,
 * which may query the database) runs on a small executor of this consumer instead.
 */
public class SqsPositionConsumer implements LifecycleObject {

    private static final Logger LOGGER = LoggerFactory.getLogger(SqsPositionConsumer.class);

    static final String REASON_ATTRIBUTE = "reason";

    private static final int MAX_MESSAGES = 10;
    private static final int WAIT_SECONDS = 20;
    private static final long PAUSE_MILLIS = 1000;
    private static final int RETRY_SECONDS = 30;
    private static final int MAX_RETRY_SECONDS = 900;
    private static final long DEVICE_CACHE_MILLIS = TimeUnit.MINUTES.toMillis(1);

    enum Action { DELETE, DEAD_LETTER, RETRY }

    record Decision(Action action, String reason) {

        static Decision delete() {
            return new Decision(Action.DELETE, null);
        }

        static Decision deadLetter(String reason) {
            return new Decision(Action.DEAD_LETTER, reason);
        }

        static Decision retry() {
            return new Decision(Action.RETRY, null);
        }
    }

    private record CachedDevice(long id, long expires) { }

    private final SqsAsyncClient sqs;
    private final ProcessingHandler processingHandler;
    private final DeviceLookupService deviceLookupService;
    private final String queueUrl;
    private final String deadLetterQueueUrl;
    private final int pollers;
    private final int pauseThreshold;
    private final long timeout;
    private final ExecutorService executor;

    private final Map<String, CachedDevice> devices = new ConcurrentHashMap<>();
    private final List<CompletableFuture<Void>> loops = new ArrayList<>();
    private final List<AtomicReference<CompletableFuture<ReceiveMessageResponse>>> receives = new ArrayList<>();
    private volatile boolean running;

    public SqsPositionConsumer(
            Config config, SqsAsyncClient sqs,
            ProcessingHandler processingHandler, DeviceLookupService deviceLookupService) {
        this.sqs = sqs;
        this.processingHandler = processingHandler;
        this.deviceLookupService = deviceLookupService;
        queueUrl = config.getString(Keys.SQS_QUEUE_URL);
        deadLetterQueueUrl = config.getString(Keys.SQS_DEAD_LETTER_QUEUE_URL);
        pollers = config.getInteger(Keys.SQS_POLLERS);
        pauseThreshold = config.getInteger(Keys.SQS_PAUSE_THRESHOLD);
        timeout = config.getLong(Keys.INGEST_TIMEOUT);
        var index = new AtomicInteger();
        executor = Executors.newFixedThreadPool(Math.max(pollers, 1), runnable -> {
            Thread thread = new Thread(runnable, "sqs-consumer-" + index.getAndIncrement());
            thread.setDaemon(true);
            return thread;
        });
    }

    @Override
    public void start() {
        running = true;
        for (int i = 0; i < pollers; i++) {
            var done = new CompletableFuture<Void>();
            var receive = new AtomicReference<CompletableFuture<ReceiveMessageResponse>>();
            loops.add(done);
            receives.add(receive);
            executor.execute(() -> poll(receive, done));
        }
        LOGGER.info("Consuming positions from {} with {} loops", queueUrl, pollers);
    }

    /**
     * Stops receiving and gives the messages in flight time to finish. An idle receive is cancelled; messages it
     * may already have taken, like the ones that do not finish in time, return to the queue after its visibility
     * timeout.
     */
    @Override
    public void stop() {
        running = false;
        receives.forEach(receive -> {
            var pending = receive.get();
            if (pending != null) {
                pending.cancel(true);
            }
        });
        try {
            CompletableFuture.allOf(loops.toArray(CompletableFuture[]::new)).get(timeout, TimeUnit.SECONDS);
        } catch (Exception e) {
            LOGGER.warn("SQS consumer stopped with messages in flight");
        }
        executor.shutdown();
        sqs.close();
    }

    private void poll(
            AtomicReference<CompletableFuture<ReceiveMessageResponse>> receive, CompletableFuture<Void> done) {
        if (!running) {
            done.complete(null);
            return;
        }
        CompletableFuture<Void> cycle;
        if (processingHandler.getPendingPositions() > pauseThreshold) {
            cycle = delay();
        } else {
            var response = sqs.receiveMessage(request -> request
                    .queueUrl(queueUrl)
                    .maxNumberOfMessages(MAX_MESSAGES)
                    .waitTimeSeconds(WAIT_SECONDS)
                    .messageSystemAttributeNames(MessageSystemAttributeName.APPROXIMATE_RECEIVE_COUNT));
            receive.set(response);
            cycle = response.thenComposeAsync(result -> handle(result.messages()), executor);
        }
        cycle.exceptionallyCompose(error -> {
            if (running) {
                LOGGER.warn("SQS consumer cycle failed", error);
                return delay();
            }
            return CompletableFuture.completedFuture(null);
        }).whenCompleteAsync((ignore, error) -> poll(receive, done), executor);
    }

    private CompletableFuture<Void> delay() {
        return CompletableFuture.runAsync(
                () -> { }, CompletableFuture.delayedExecutor(PAUSE_MILLIS, TimeUnit.MILLISECONDS, executor));
    }

    /** Processes the messages of one receive and settles them with the queue once all have a decision. */
    CompletableFuture<Void> handle(List<Message> messages) {
        if (messages.isEmpty()) {
            return CompletableFuture.completedFuture(null);
        }
        List<CompletableFuture<Decision>> decisions = messages.stream().map(this::handle).toList();
        return CompletableFuture.allOf(decisions.toArray(CompletableFuture[]::new))
                .thenComposeAsync(ignore -> settle(messages, decisions.stream().map(CompletableFuture::join).toList()),
                        executor);
    }

    private CompletableFuture<Decision> handle(Message message) {
        OsmAndPositionBatch.Batch batch;
        try {
            batch = OsmAndPositionBatch.parse(message.body());
        } catch (RuntimeException e) {
            return CompletableFuture.completedFuture(Decision.deadLetter("invalid body"));
        }
        long deviceId;
        try {
            deviceId = lookup(batch.deviceId());
        } catch (SecurityException e) {
            return CompletableFuture.completedFuture(Decision.deadLetter(e.getMessage()));
        }
        if (deviceId == 0) {
            return CompletableFuture.completedFuture(Decision.deadLetter("unknown device"));
        }

        List<Position> positions = batch.positions();
        List<CompletableFuture<IngestOutcome>> outcomes = new ArrayList<>();
        for (Position position : positions) {
            position.setDeviceId(deviceId);
            position.setProtocol("osmand");
            outcomes.add(position.getIngestTicket().getOutcome()
                    .completeOnTimeout(IngestOutcome.failed("timeout"), timeout, TimeUnit.SECONDS));
        }
        positions.forEach(processingHandler::process);
        return CompletableFuture.allOf(outcomes.toArray(CompletableFuture[]::new))
                .thenApply(ignore -> decide(batch));
    }

    /**
     * Retries the message while any of its positions lacks a final outcome. Items rejected while parsing (bad
     * time, coordinates or id) will never pass, so the message goes to the dead-letter queue; its other positions
     * are already final and come back as duplicates if it is redriven.
     */
    static Decision decide(OsmAndPositionBatch.Batch batch) {
        List<String> rejected = new ArrayList<>();
        for (var item : batch.items()) {
            if (item.position() != null && !item.position().getIngestTicket().getOutcome().join().status().isFinal()) {
                return Decision.retry();
            }
            if (item.rejection() != null) {
                rejected.add("item " + item.index() + ": " + item.rejection().reason());
            }
        }
        return rejected.isEmpty() ? Decision.delete() : Decision.deadLetter(String.join("; ", rejected));
    }

    /** Device id for a unique id, 0 when unknown; throws when the device is disabled or expired. */
    private long lookup(String uniqueId) {
        long now = System.currentTimeMillis();
        CachedDevice cached = devices.get(uniqueId);
        if (cached != null && cached.expires() > now) {
            return cached.id();
        }
        Device device = deviceLookupService.lookup(new String[] {uniqueId});
        if (device == null) {
            return 0;
        }
        device.checkDisabled();
        devices.put(uniqueId, new CachedDevice(device.getId(), now + DEVICE_CACHE_MILLIS));
        return device.getId();
    }

    private CompletableFuture<Void> settle(List<Message> messages, List<Decision> decisions) {
        List<Message> delete = new ArrayList<>();
        List<Message> retry = new ArrayList<>();
        List<Message> deadLetter = new ArrayList<>();
        List<String> reasons = new ArrayList<>();
        for (int i = 0; i < messages.size(); i++) {
            Decision decision = decisions.get(i);
            switch (decision.action()) {
                case DELETE -> delete.add(messages.get(i));
                case RETRY -> retry.add(messages.get(i));
                case DEAD_LETTER -> {
                    deadLetter.add(messages.get(i));
                    reasons.add(decision.reason());
                }
            }
        }
        return deadLetter(deadLetter, reasons)
                .thenCompose(sent -> {
                    delete.addAll(sent);
                    return CompletableFuture.allOf(delete(delete), retry(retry));
                });
    }

    /** Copies the messages to the dead-letter queue; returns the ones copied, which may leave the main queue. */
    private CompletableFuture<List<Message>> deadLetter(List<Message> messages, List<String> reasons) {
        if (messages.isEmpty()) {
            return CompletableFuture.completedFuture(List.of());
        }
        for (int i = 0; i < messages.size(); i++) {
            LOGGER.warn("SQS message {} is dead: {}", messages.get(i).messageId(), reasons.get(i));
        }
        if (deadLetterQueueUrl == null) {
            return CompletableFuture.completedFuture(List.of()); // left for the redrive policy
        }
        List<SendMessageBatchRequestEntry> entries = new ArrayList<>();
        for (int i = 0; i < messages.size(); i++) {
            entries.add(SendMessageBatchRequestEntry.builder()
                    .id(String.valueOf(i))
                    .messageBody(messages.get(i).body())
                    .messageAttributes(Map.of(REASON_ATTRIBUTE, MessageAttributeValue.builder()
                            .dataType("String").stringValue(reasons.get(i)).build()))
                    .build());
        }
        return sqs.sendMessageBatch(request -> request.queueUrl(deadLetterQueueUrl).entries(entries))
                .thenApply(response -> {
                    response.failed().forEach(entry -> LOGGER.warn(
                            "SQS dead-letter send failed: {} {}", entry.code(), entry.message()));
                    return response.successful().stream()
                            .map(entry -> messages.get(Integer.parseInt(entry.id())))
                            .toList();
                });
    }

    private CompletableFuture<Void> delete(List<Message> messages) {
        if (messages.isEmpty()) {
            return CompletableFuture.completedFuture(null);
        }
        List<DeleteMessageBatchRequestEntry> entries = new ArrayList<>();
        for (int i = 0; i < messages.size(); i++) {
            entries.add(DeleteMessageBatchRequestEntry.builder()
                    .id(String.valueOf(i)).receiptHandle(messages.get(i).receiptHandle()).build());
        }
        // A message that fails to be deleted comes back and its positions are recognized as duplicates
        return sqs.deleteMessageBatch(request -> request.queueUrl(queueUrl).entries(entries))
                .thenAccept(response -> response.failed().forEach(entry -> LOGGER.warn(
                        "SQS delete failed: {} {}", entry.code(), entry.message())));
    }

    /** Postpones the next attempt of the messages, longer for each failed receive. */
    private CompletableFuture<Void> retry(List<Message> messages) {
        if (messages.isEmpty()) {
            return CompletableFuture.completedFuture(null);
        }
        List<ChangeMessageVisibilityBatchRequestEntry> entries = new ArrayList<>();
        for (int i = 0; i < messages.size(); i++) {
            Message message = messages.get(i);
            entries.add(ChangeMessageVisibilityBatchRequestEntry.builder()
                    .id(String.valueOf(i))
                    .receiptHandle(message.receiptHandle())
                    .visibilityTimeout(retryDelay(receiveCount(message)))
                    .build());
        }
        return sqs.changeMessageVisibilityBatch(request -> request.queueUrl(queueUrl).entries(entries))
                .thenAccept(response -> response.failed().forEach(entry -> LOGGER.warn(
                        "SQS visibility change failed: {} {}", entry.code(), entry.message())));
    }

    private static int receiveCount(Message message) {
        try {
            return Integer.parseInt(message.attributes().get(MessageSystemAttributeName.APPROXIMATE_RECEIVE_COUNT));
        } catch (NumberFormatException e) {
            return 1;
        }
    }

    static int retryDelay(int receiveCount) {
        int shift = Math.min(Math.max(receiveCount - 1, 0), 5);
        return Math.min(RETRY_SECONDS << shift, MAX_RETRY_SECONDS);
    }

}
