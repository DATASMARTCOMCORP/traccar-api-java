package org.traccar.sqs;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.traccar.ProcessingHandler;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.database.DeviceLookupService;
import org.traccar.database.IngestOutcome;
import org.traccar.model.Device;
import org.traccar.model.Position;
import software.amazon.awssdk.auth.credentials.AwsBasicCredentials;
import software.amazon.awssdk.auth.credentials.StaticCredentialsProvider;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.sqs.SqsAsyncClient;
import software.amazon.awssdk.services.sqs.model.QueueAttributeName;

import java.net.URI;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.BooleanSupplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

/**
 * Runs the consumer against a real SQS-compatible server, with the processing mocked. Opt-in: set SQS_ENDPOINT,
 * for example to a local ElasticMQ ({@code docker run -p 9324:9324 softwaremill/elasticmq-native}).
 */
@EnabledIfEnvironmentVariable(named = "SQS_ENDPOINT", matches = ".+")
class SqsPositionConsumerIntegrationTest {

    private static SqsAsyncClient client() {
        return SqsAsyncClient.builder()
                .endpointOverride(URI.create(System.getenv("SQS_ENDPOINT")))
                .region(Region.US_EAST_1)
                .credentialsProvider(StaticCredentialsProvider.create(AwsBasicCredentials.create("test", "test")))
                .build();
    }

    private static void waitFor(BooleanSupplier condition) throws InterruptedException {
        long deadline = System.currentTimeMillis() + 30_000;
        while (!condition.getAsBoolean()) {
            assertTrue(System.currentTimeMillis() < deadline, "condition not reached in time");
            Thread.sleep(200);
        }
    }

    private static int count(SqsAsyncClient admin, String queueUrl) {
        var attributes = admin.getQueueAttributes(request -> request.queueUrl(queueUrl).attributeNames(
                QueueAttributeName.APPROXIMATE_NUMBER_OF_MESSAGES,
                QueueAttributeName.APPROXIMATE_NUMBER_OF_MESSAGES_NOT_VISIBLE)).join().attributes();
        return Integer.parseInt(attributes.get(QueueAttributeName.APPROXIMATE_NUMBER_OF_MESSAGES))
                + Integer.parseInt(attributes.get(QueueAttributeName.APPROXIMATE_NUMBER_OF_MESSAGES_NOT_VISIBLE));
    }

    @Test
    void consumesDeletesAndDeadLetters() throws Exception {
        try (var admin = client()) {
            String suffix = UUID.randomUUID().toString().substring(0, 8);
            String queueUrl = admin.createQueue(request -> request.queueName("positions-" + suffix)
                    .attributes(Map.of(QueueAttributeName.VISIBILITY_TIMEOUT, "60"))).join().queueUrl();
            String deadUrl = admin.createQueue(request -> request.queueName("positions-dead-" + suffix))
                    .join().queueUrl();

            var config = new Config();
            config.setString(Keys.SQS_QUEUE_URL, queueUrl);
            config.setString(Keys.SQS_DEAD_LETTER_QUEUE_URL, deadUrl);

            var device = new Device();
            device.setId(7);
            device.setUniqueId("phone");
            var deviceLookupService = mock(DeviceLookupService.class);
            when(deviceLookupService.lookup(any())).thenReturn(device);
            var processingHandler = mock(ProcessingHandler.class);
            List<String> processed = new CopyOnWriteArrayList<>();
            doAnswer(invocation -> {
                Position position = invocation.getArgument(0);
                processed.add(position.getIngestTicket().getEventId());
                position.getIngestTicket().complete(IngestOutcome.stored(1));
                return null;
            }).when(processingHandler).process(any());

            var consumer = new SqsPositionConsumer(config, client(), processingHandler, deviceLookupService);
            consumer.start();
            try {
                admin.sendMessage(request -> request.queueUrl(queueUrl).messageBody(
                        "{\"deviceId\":\"phone\",\"positions\":[{\"id\":\"e1\",\"timestamp\":1700000000,"
                                + "\"lat\":-22,\"lon\":-43}]}")).join();
                admin.sendMessage(request -> request.queueUrl(queueUrl).messageBody("not json")).join();

                waitFor(() -> count(admin, queueUrl) == 0 && count(admin, deadUrl) == 1);
                assertEquals(List.of("e1"), processed);

                var dead = admin.receiveMessage(request -> request.queueUrl(deadUrl)
                        .messageAttributeNames(SqsPositionConsumer.REASON_ATTRIBUTE)).join().messages();
                assertEquals("not json", dead.getFirst().body());
                assertEquals("invalid body",
                        dead.getFirst().messageAttributes().get(SqsPositionConsumer.REASON_ATTRIBUTE).stringValue());
            } finally {
                long started = System.currentTimeMillis();
                consumer.stop();
                // Idle long polls are cancelled instead of waited for
                assertTrue(System.currentTimeMillis() - started < 5_000);
            }
        }
    }

}
