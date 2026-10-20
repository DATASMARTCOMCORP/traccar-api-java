package org.traccar.sqs;

import org.traccar.config.Config;
import org.traccar.config.Keys;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.sqs.SqsAsyncClient;

import java.net.URI;

public final class SqsClients {

    private SqsClients() {
    }

    /** Async client for the configured region and endpoint; credentials come from the default AWS chain. */
    public static SqsAsyncClient create(Config config) {
        var builder = SqsAsyncClient.builder();
        if (config.hasKey(Keys.SQS_REGION)) {
            builder.region(Region.of(config.getString(Keys.SQS_REGION)));
        }
        if (config.hasKey(Keys.SQS_ENDPOINT)) {
            builder.endpointOverride(URI.create(config.getString(Keys.SQS_ENDPOINT)));
        }
        return builder.build();
    }

}
