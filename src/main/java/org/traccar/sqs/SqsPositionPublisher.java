package org.traccar.sqs;

import io.netty.buffer.Unpooled;
import io.netty.channel.ChannelFutureListener;
import io.netty.channel.ChannelHandler;
import io.netty.channel.ChannelHandlerContext;
import io.netty.channel.SimpleChannelInboundHandler;
import io.netty.handler.codec.http.DefaultFullHttpResponse;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.handler.codec.http.HttpHeaderValues;
import io.netty.handler.codec.http.HttpMethod;
import io.netty.handler.codec.http.HttpResponseStatus;
import io.netty.handler.codec.http.HttpUtil;
import io.netty.handler.codec.http.HttpVersion;
import io.netty.handler.codec.http.QueryStringDecoder;
import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.json.JsonObjectBuilder;
import jakarta.json.JsonValue;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.protocol.OsmAndPositionBatch;
import software.amazon.awssdk.services.sqs.SqsAsyncClient;

import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;

/**
 * Accepts OsmAnd reports (form or query, and the JSON batch at {@value #BATCH_PATH}) and publishes each request as
 * one SQS message in the batch format, answering only after SQS accepted it. It checks the format only: whether the
 * device exists is the consumer's concern, so this works while the database is down.
 *
 * <p>Nothing blocks the event loop: the request is parsed in place and the answer is written from the callback of
 * the async publish.
 */
@ChannelHandler.Sharable
public class SqsPositionPublisher extends SimpleChannelInboundHandler<FullHttpRequest> {

    private static final Logger LOGGER = LoggerFactory.getLogger(SqsPositionPublisher.class);

    static final String BATCH_PATH = "/api/positions/batch";
    static final String HEALTH_PATH = "/health";

    private static final DateTimeFormatter DATE_FORMAT = DateTimeFormatter
            .ofPattern("yyyy-MM-dd HH:mm:ss").withZone(ZoneId.systemDefault());

    private final SqsAsyncClient sqs;
    private final String queueUrl;

    public SqsPositionPublisher(SqsAsyncClient sqs, String queueUrl) {
        this.sqs = sqs;
        this.queueUrl = queueUrl;
    }

    @Override
    protected void channelRead0(ChannelHandlerContext ctx, FullHttpRequest request) {
        boolean keepAlive = HttpUtil.isKeepAlive(request);
        String path = new QueryStringDecoder(request.uri()).path();
        if (path.equals(HEALTH_PATH)) {
            respond(ctx, keepAlive, HttpResponseStatus.OK, null);
        } else if (!request.method().equals(HttpMethod.POST)) {
            respond(ctx, keepAlive, HttpResponseStatus.METHOD_NOT_ALLOWED, null);
        } else if (path.equals(BATCH_PATH)) {
            publishBatch(ctx, keepAlive, request.content().toString(StandardCharsets.UTF_8));
        } else {
            publishForm(ctx, keepAlive, request);
        }
    }

    @Override
    public void exceptionCaught(ChannelHandlerContext ctx, Throwable cause) {
        LOGGER.warn("SQS publisher connection error", cause);
        ctx.close();
    }

    private void publishForm(ChannelHandlerContext ctx, boolean keepAlive, FullHttpRequest request) {
        Map<String, List<String>> params = new QueryStringDecoder(request.uri()).parameters();
        if (params.isEmpty()) {
            params = new QueryStringDecoder(request.content().toString(StandardCharsets.US_ASCII), false).parameters();
        }
        JsonObject message;
        try {
            message = fromForm(params, System.currentTimeMillis());
        } catch (RuntimeException e) {
            respond(ctx, keepAlive, HttpResponseStatus.BAD_REQUEST, null);
            return;
        }
        publish(message.toString()).whenComplete((ignore, error) -> respond(ctx, keepAlive,
                error == null ? HttpResponseStatus.OK : HttpResponseStatus.SERVICE_UNAVAILABLE, null));
    }

    /**
     * Publishes the valid items of a batch. Invalid items get their final answer right away, as from the server;
     * the others are answered as queued, which is final for the sender too: SQS now holds them.
     */
    private void publishBatch(ChannelHandlerContext ctx, boolean keepAlive, String body) {
        JsonObject root;
        OsmAndPositionBatch.Batch batch;
        try (var reader = Json.createReader(new StringReader(body))) {
            root = reader.readObject();
            batch = OsmAndPositionBatch.parse(body);
        } catch (RuntimeException e) {
            respond(ctx, keepAlive, HttpResponseStatus.BAD_REQUEST, Json.createObjectBuilder()
                    .add("accepted", 0).add("error", "Corpo do lote inválido.").build());
            return;
        }

        var values = root.getJsonArray("positions");
        var queued = Json.createArrayBuilder();
        var results = Json.createArrayBuilder();
        boolean empty = true;
        for (var item : batch.items()) {
            var result = Json.createObjectBuilder().add("index", item.index());
            if (item.id() != null) {
                result.add("id", item.id());
            }
            if (item.rejection() != null) {
                result.add("status", "rejected").add("reason", item.rejection().reason());
            } else {
                result.add("status", "queued");
                if (item.position() != null) {
                    queued.add(values.get(item.index()));
                    empty = false;
                }
            }
            results.add(result);
        }
        JsonObject response = Json.createObjectBuilder()
                .add("accepted", batch.items().size()).add("results", results).build();
        if (empty) {
            respond(ctx, keepAlive, HttpResponseStatus.OK, response);
            return;
        }
        String message = Json.createObjectBuilder(root).add("positions", queued).build().toString();
        publish(message).whenComplete((ignore, error) -> {
            if (error == null) {
                respond(ctx, keepAlive, HttpResponseStatus.OK, response);
            } else {
                respond(ctx, keepAlive, HttpResponseStatus.SERVICE_UNAVAILABLE, Json.createObjectBuilder()
                        .add("accepted", 0).add("error", "Fila indisponível; tente novamente.").build());
            }
        });
    }

    private CompletableFuture<?> publish(String body) {
        CompletableFuture<?> result;
        try {
            result = sqs.sendMessage(request -> request.queueUrl(queueUrl).messageBody(body));
        } catch (RuntimeException e) {
            result = CompletableFuture.failedFuture(e);
        }
        return result.whenComplete((ignore, error) -> {
            if (error != null) {
                LOGGER.warn("SQS publish failed", error);
            }
        });
    }

    /**
     * Converts an OsmAnd form report into a one-item batch, with the attributes the batch format carries. A report
     * without coordinates or with an invalid value is refused, since the consumer could never store it.
     */
    static JsonObject fromForm(Map<String, List<String>> params, long now) {
        String deviceId = first(params, "id", "deviceid");
        if (deviceId == null || deviceId.isBlank()) {
            throw new IllegalArgumentException("Missing device id");
        }
        var item = Json.createObjectBuilder();
        String reportId = first(params, "reportId");
        if (reportId != null) {
            item.add("id", reportId.trim());
        }
        String timestamp = first(params, "timestamp");
        item.add("timestamp", timestamp != null ? timestamp(timestamp) : Json.createValue(now));
        String location = first(params, "location");
        if (location != null) {
            String[] parts = location.split(",");
            item.add("lat", Double.parseDouble(parts[0])).add("lon", Double.parseDouble(parts[1]));
        } else {
            item.add("lat", Double.parseDouble(first(params, "lat")))
                    .add("lon", Double.parseDouble(first(params, "lon")));
        }
        number(params, item, "speed", "speed");
        number(params, item, "heading", "bearing", "heading");
        number(params, item, "altitude", "altitude");
        number(params, item, "accuracy", "accuracy");
        number(params, item, "batt", "batt");
        for (String key : List.of("charge", "is_moving")) {
            String value = first(params, key);
            if (value != null) {
                item.add(key, Boolean.parseBoolean(value) || "1".equals(value));
            }
        }
        String activity = first(params, "activity");
        if (activity != null) {
            item.add("activity", activity);
        }

        var root = Json.createObjectBuilder().add("deviceId", deviceId.trim());
        for (String key : List.of("installId", "os")) {
            String value = first(params, key);
            if (value != null) {
                root.add(key, value);
            }
        }
        JsonObject message = root.add("positions", Json.createArrayBuilder().add(item)).build();
        var rejection = OsmAndPositionBatch.parse(message.toString()).items().getFirst().rejection();
        if (rejection != null) {
            throw new IllegalArgumentException(rejection.reason());
        }
        return message;
    }

    private static JsonValue timestamp(String value) {
        if (value.chars().allMatch(Character::isDigit)) {
            return Json.createValue(Long.parseLong(value));
        }
        if (value.contains("T")) {
            return Json.createValue(value);
        }
        return Json.createValue(LocalDateTime.parse(value, DATE_FORMAT)
                .atZone(ZoneId.systemDefault()).toInstant().toEpochMilli());
    }

    private static void number(Map<String, List<String>> params, JsonObjectBuilder item, String key, String... names) {
        String value = first(params, names);
        if (value != null) {
            item.add(key, Double.parseDouble(value));
        }
    }

    private static String first(Map<String, List<String>> params, String... names) {
        for (String name : names) {
            List<String> values = params.get(name);
            if (values != null && !values.isEmpty()) {
                return values.getFirst();
            }
        }
        return null;
    }

    private static void respond(
            ChannelHandlerContext ctx, boolean keepAlive, HttpResponseStatus status, JsonObject body) {
        var content = body != null
                ? Unpooled.copiedBuffer(body.toString(), StandardCharsets.UTF_8) : Unpooled.EMPTY_BUFFER;
        var response = new DefaultFullHttpResponse(HttpVersion.HTTP_1_1, status, content);
        if (body != null) {
            response.headers().set(HttpHeaderNames.CONTENT_TYPE, HttpHeaderValues.APPLICATION_JSON);
        }
        HttpUtil.setContentLength(response, content.readableBytes());
        HttpUtil.setKeepAlive(response, keepAlive);
        var future = ctx.writeAndFlush(response);
        if (!keepAlive) {
            future.addListener(ChannelFutureListener.CLOSE);
        }
    }

}
