/*
 * Copyright 2013 - 2025 Anton Tananaev (anton@traccar.org)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package org.traccar.protocol;

import io.netty.buffer.Unpooled;
import io.netty.channel.Channel;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.handler.codec.http.DefaultFullHttpResponse;
import io.netty.handler.codec.http.HttpMethod;
import io.netty.handler.codec.http.HttpVersion;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.handler.codec.http.HttpHeaderValues;
import io.netty.handler.codec.http.HttpResponseStatus;
import io.netty.handler.codec.http.QueryStringDecoder;
import jakarta.json.Json;
import jakarta.json.JsonNumber;
import jakarta.json.JsonObject;
import jakarta.json.JsonObjectBuilder;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import org.traccar.BaseHttpProtocolDecoder;
import org.traccar.config.Keys;
import org.traccar.helper.UnitsConverter;
import org.traccar.session.DeviceSession;
import org.traccar.Protocol;
import org.traccar.NetworkMessage;
import org.traccar.helper.DateUtil;
import org.traccar.database.IngestOutcome;
import org.traccar.database.IngestTicket;
import org.traccar.model.CellTower;
import org.traccar.model.Command;
import org.traccar.model.Network;
import org.traccar.model.Position;
import org.traccar.model.WifiAccessPoint;

import java.io.StringReader;
import java.net.SocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.time.Instant;
import java.util.Date;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

public class OsmAndProtocolDecoder extends BaseHttpProtocolDecoder {

    private static final DateTimeFormatter DATE_FORMAT = DateTimeFormatter
            .ofPattern("yyyy-MM-dd HH:mm:ss").withZone(ZoneId.systemDefault());

    private double minAccuracy;

    public OsmAndProtocolDecoder(Protocol protocol) {
        super(protocol);
    }

    @Override
    protected void init() {
        minAccuracy = getConfig().getDouble(Keys.OSMAND_MIN_ACCURACY);
    }

    @Override
    protected Object decode(Channel channel, SocketAddress remoteAddress, Object msg) throws Exception {

        FullHttpRequest request = (FullHttpRequest) msg;
        if (new QueryStringDecoder(request.uri()).path().equals("/api/positions/batch")) {
            return decodeBatch(channel, remoteAddress, request);
        }
        String contentType = request.headers().get(HttpHeaderNames.CONTENT_TYPE);
        if (contentType != null && contentType.startsWith(HttpHeaderValues.APPLICATION_JSON.toString())) {
            return decodeJson(channel, remoteAddress, request);
        } else {
            return decodeQuery(channel, remoteAddress, request);
        }
    }

    private Object decodeBatch(Channel channel, SocketAddress remoteAddress, FullHttpRequest request) {
        HttpResponseStatus status = HttpResponseStatus.OK;
        String error = null;
        try {
            if (!request.method().equals(HttpMethod.POST)) {
                throw new IllegalArgumentException("Use POST.");
            }
            Instant now = Instant.now();
            var batch = OsmAndPositionBatch.parse(
                    request.content().toString(StandardCharsets.UTF_8),
                    now.minusSeconds(getConfig().getLong(Keys.INGEST_MAX_AGE)),
                    now.plusSeconds(getConfig().getLong(Keys.INGEST_MAX_FUTURE)));
            DeviceSession session = getDeviceSession(channel, remoteAddress, batch.deviceId());
            if (session == null) {
                status = HttpResponseStatus.UNPROCESSABLE_ENTITY;
                error = "Aparelho desconhecido ou desativado.";
            } else if (!OsmAndPositionBatch.allow(
                    session.getDeviceId(), getConfig().getInteger(Keys.INGEST_BATCHES_PER_MINUTE))) {
                status = HttpResponseStatus.TOO_MANY_REQUESTS;
                error = "Lotes demais deste aparelho; tente novamente em instantes.";
            } else {
                List<Position> positions = batch.positions();
                for (Position position : positions) {
                    position.setDeviceId(session.getDeviceId());
                    position.setProtocol(getProtocolName());
                }
                respondWhenProcessed(channel, remoteAddress, batch);
                return positions.isEmpty() ? null : positions;
            }
        } catch (RuntimeException e) {
            status = HttpResponseStatus.BAD_REQUEST;
            error = "Corpo do lote inválido.";
        }
        sendBatchResponse(channel, remoteAddress, status, Json.createObjectBuilder()
                .add("accepted", 0)
                .add("error", error));
        return null;
    }

    /**
     * Answers once every item has an outcome. {@code accepted} counts the leading items with a final outcome,
     * which the sender may delete; {@code results} has the outcome of each item. The status is 200 when every
     * item is final and 503 otherwise, so a sender that only looks at the status retries the whole batch.
     */
    private void respondWhenProcessed(Channel channel, SocketAddress remoteAddress, OsmAndPositionBatch.Batch batch) {
        long timeout = getConfig().getLong(Keys.INGEST_TIMEOUT);
        var futures = new java.util.ArrayList<CompletableFuture<IngestOutcome>>();
        for (var item : batch.items()) {
            if (item.position() != null) {
                futures.add(item.position().getIngestTicket().getOutcome()
                        .completeOnTimeout(IngestOutcome.failed("timeout"), timeout, TimeUnit.SECONDS));
            }
        }
        CompletableFuture.allOf(futures.toArray(CompletableFuture[]::new)).whenComplete((ignore, e) -> {
            var outcomes = new java.util.HashMap<OsmAndPositionBatch.Item, IngestOutcome>();
            for (var item : batch.items()) {
                outcomes.put(item, outcome(item, outcomes));
            }
            int accepted = 0;
            boolean complete = true;
            var results = Json.createArrayBuilder();
            for (var item : batch.items()) {
                IngestOutcome outcome = outcomes.get(item);
                if (outcome.status().isFinal()) {
                    accepted += complete ? 1 : 0;
                } else {
                    complete = false;
                }
                var result = Json.createObjectBuilder()
                        .add("index", item.index())
                        .add("status", outcome.status().name().toLowerCase());
                if (item.id() != null) {
                    result.add("id", item.id());
                }
                if (outcome.positionId() != 0) {
                    result.add("positionId", outcome.positionId());
                }
                if (outcome.reason() != null) {
                    result.add("reason", outcome.reason());
                }
                results.add(result);
            }
            var body = Json.createObjectBuilder().add("accepted", accepted).add("results", results);
            sendBatchResponse(channel, remoteAddress, complete
                    ? HttpResponseStatus.OK : HttpResponseStatus.SERVICE_UNAVAILABLE, body);
        });
    }

    private static IngestOutcome outcome(
            OsmAndPositionBatch.Item item, Map<OsmAndPositionBatch.Item, IngestOutcome> earlier) {
        if (item.rejection() != null) {
            return item.rejection();
        }
        if (item.position() != null) {
            return item.position().getIngestTicket().getOutcome().join();
        }
        // A repeated id within the batch shares the fate of its first occurrence
        IngestOutcome original = earlier.get(item.original());
        return switch (original.status()) {
            case STORED, DUPLICATE -> IngestOutcome.duplicate(original.positionId());
            default -> original;
        };
    }

    private void sendBatchResponse(
            Channel channel, SocketAddress remoteAddress, HttpResponseStatus status, JsonObjectBuilder body) {
        if (channel != null) {
            var response = new DefaultFullHttpResponse(
                    HttpVersion.HTTP_1_1, status,
                    Unpooled.copiedBuffer(body.build().toString(), StandardCharsets.UTF_8));
            response.headers().set(HttpHeaderNames.CONTENT_TYPE, "application/json");
            response.headers().setInt(HttpHeaderNames.CONTENT_LENGTH, response.content().readableBytes());
            channel.writeAndFlush(new NetworkMessage(response, remoteAddress));
        }
    }

    private Object decodeQuery(
            Channel channel, SocketAddress remoteAddress, FullHttpRequest request) throws Exception {

        QueryStringDecoder decoder = new QueryStringDecoder(request.uri());
        Map<String, List<String>> params = decoder.parameters();
        if (params.isEmpty()) {
            decoder = new QueryStringDecoder(request.content().toString(StandardCharsets.US_ASCII), false);
            params = decoder.parameters();
        }

        Position position = new Position(getProtocolName());
        position.setValid(true);

        Network network = new Network();
        Double latitude = null;
        Double longitude = null;
        String reportId = null;

        for (Map.Entry<String, List<String>> entry : params.entrySet()) {
            for (String value : entry.getValue()) {
                switch (entry.getKey()) {
                    case "id", "deviceid" -> {
                        DeviceSession deviceSession = getDeviceSession(channel, remoteAddress, value);
                        if (deviceSession == null) {
                            sendResponse(channel, HttpResponseStatus.BAD_REQUEST);
                            return null;
                        }
                        position.setDeviceId(deviceSession.getDeviceId());
                    }
                    case "notificationToken" -> {
                        if (position.getDeviceId() > 0) {
                            getCommandsManager().updateNotificationToken(position.getDeviceId(), value);
                        }
                    }
                    case "reportId" -> reportId = value.trim();
                    case "valid" -> position.setValid(Boolean.parseBoolean(value) || "1".equals(value));
                    case "timestamp" -> {
                        try {
                            long timestamp = Long.parseLong(value);
                            if (timestamp < Integer.MAX_VALUE) {
                                timestamp *= 1000;
                            }
                            position.setTime(new Date(timestamp));
                        } catch (NumberFormatException error) {
                            if (value.contains("T")) {
                                position.setTime(DateUtil.parseDate(value));
                            } else {
                                position.setTime(DateUtil.parse(DATE_FORMAT, value));
                            }
                        }
                    }
                    case "lat" -> latitude = Double.parseDouble(value);
                    case "lon" -> longitude = Double.parseDouble(value);
                    case "location" -> {
                        String[] location = value.split(",");
                        latitude = Double.parseDouble(location[0]);
                        longitude = Double.parseDouble(location[1]);
                    }
                    case "cell" -> {
                        String[] cell = value.split(",");
                        if (cell.length > 4) {
                            network.addCellTower(CellTower.from(
                                    Integer.parseInt(cell[0]), Integer.parseInt(cell[1]),
                                    Integer.parseInt(cell[2]), Integer.parseInt(cell[3]), Integer.parseInt(cell[4])));
                        } else {
                            network.addCellTower(CellTower.from(
                                    Integer.parseInt(cell[0]), Integer.parseInt(cell[1]),
                                    Integer.parseInt(cell[2]), Integer.parseInt(cell[3])));
                        }
                    }
                    case "wifi" -> {
                        String[] wifi = value.split(",");
                        network.addWifiAccessPoint(WifiAccessPoint.from(
                                wifi[0].replace('-', ':'), Integer.parseInt(wifi[1])));
                    }
                    case "speed" -> position.setSpeed(convertSpeed(Double.parseDouble(value), "kn"));
                    case "bearing", "heading" -> position.setCourse(Double.parseDouble(value));
                    case "altitude" -> position.setAltitude(Double.parseDouble(value));
                    case "accuracy" -> position.setAccuracy(Double.parseDouble(value));
                    case "hdop" -> position.set(Position.KEY_HDOP, Double.parseDouble(value));
                    case "batt" -> position.set(Position.KEY_BATTERY_LEVEL, Double.parseDouble(value));
                    case "driverUniqueId" -> position.set(Position.KEY_DRIVER_UNIQUE_ID, value);
                    case "charge" -> position.set(Position.KEY_CHARGE, Boolean.parseBoolean(value));
                    default -> {
                        try {
                            position.set(entry.getKey(), Double.parseDouble(value));
                        } catch (NumberFormatException e) {
                            switch (value) {
                                case "true" -> position.set(entry.getKey(), true);
                                case "false" -> position.set(entry.getKey(), false);
                                default -> position.set(entry.getKey(), value);
                            }
                        }
                    }
                }
            }
        }

        boolean timeReported = position.getFixTime() != null;
        if (!timeReported) {
            position.setTime(new Date());
        }

        if (network.getCellTowers() != null || network.getWifiAccessPoints() != null) {
            position.setNetwork(network);
        }

        if (latitude != null && longitude != null) {
            position.setLatitude(latitude);
            position.setLongitude(longitude);
            if (timeReported) {
                setLiveTicket(position, reportId);
            }
        } else {
            getLastLocation(position, position.getDeviceTime());
        }

        if (position.getDeviceId() != 0) {
            String response = null;
            for (Command command : getCommandsManager().readQueuedCommands(position.getDeviceId(), 1)) {
                response = command.getString(Command.KEY_DATA);
            }
            if (response != null) {
                sendResponse(channel, HttpResponseStatus.OK, Unpooled.copiedBuffer(response, StandardCharsets.UTF_8));
            } else {
                sendResponse(channel, HttpResponseStatus.OK);
            }
            return position;
        } else {
            sendResponse(channel, HttpResponseStatus.BAD_REQUEST);
            return null;
        }
    }

    /**
     * Gives a live report the identity it would have in an offline batch, so it is stored once even when the
     * client also queues it. Without a report id the identity is the fix itself (time and coordinates).
     */
    private void setLiveTicket(Position position, String reportId) {
        if (getConfig().getBoolean(Keys.INGEST_LIVE_RECEIPTS)) {
            boolean usable = reportId != null && !reportId.isEmpty()
                    && reportId.length() <= OsmAndPositionBatch.MAX_ID_LENGTH;
            String id = usable ? reportId : IngestTicket.fixId(
                    position.getFixTime().getTime(), position.getLatitude(), position.getLongitude());
            position.setIngestTicket(new IngestTicket(id));
        }
    }

    private Object decodeJson(
            Channel channel, SocketAddress remoteAddress, FullHttpRequest request) throws Exception {

        String content = request.content().toString(StandardCharsets.UTF_8);
        JsonObject root = Json.createReader(new StringReader(content)).readObject();

        DeviceSession deviceSession = getDeviceSession(channel, remoteAddress, root.getString("device_id"));
        if (deviceSession == null) {
            sendResponse(channel, HttpResponseStatus.NOT_FOUND);
            return null;
        }

        Position position = new Position(getProtocolName());
        position.setDeviceId(deviceSession.getDeviceId());

        JsonObject location = root.getJsonObject("location");

        position.setTime(DateUtil.parseDate(location.getString("timestamp")));

        if (location.containsKey("coords")) {
            JsonObject coordinates = location.getJsonObject("coords");
            position.setValid(true);
            position.setLatitude(coordinates.getJsonNumber("latitude").doubleValue());
            position.setLongitude(coordinates.getJsonNumber("longitude").doubleValue());
            double speed = coordinates.getJsonNumber("speed").doubleValue();
            if (speed >= 0) {
                position.setSpeed(UnitsConverter.knotsFromMps(speed));
            }
            double heading = coordinates.getJsonNumber("heading").doubleValue();
            if (heading >= 0) {
                position.setCourse(heading);
            }
            double accuracy = coordinates.getJsonNumber("accuracy").doubleValue();
            if (accuracy >= minAccuracy) {
                position.setAccuracy(accuracy);
            }
            position.setAltitude(coordinates.getJsonNumber("altitude").doubleValue());
            if (location.get("uuid") instanceof JsonString uuid) {
                setLiveTicket(position, uuid.getString().trim());
            }
        } else {
            getLastLocation(position, null);
        }

        if (location.containsKey("event")) {
            position.set(Position.KEY_EVENT, location.getString("event"));
        }
        if (location.containsKey("is_moving")) {
            position.set(Position.KEY_MOTION, location.getBoolean("is_moving"));
        }
        if (location.containsKey("odometer")) {
            position.set(Position.KEY_ODOMETER, location.getInt("odometer"));
        }
        if (location.containsKey("mock")) {
            position.set("mock", location.getBoolean("mock"));
        }
        if (location.containsKey("activity")) {
            position.set("activity", location.getJsonObject("activity").getString("type"));
        }
        if (location.containsKey("battery")) {
            JsonObject battery = location.getJsonObject("battery");
            double level = battery.getJsonNumber("level").doubleValue();
            if (level >= 0) {
                position.set(Position.KEY_BATTERY_LEVEL, (int) (level * 100));
            }
            if (battery.getBoolean("is_charging")) {
                position.set(Position.KEY_CHARGE, true);
            }
        }
        if (location.containsKey("alarm")) {
            position.set(Position.KEY_ALARM, location.getString("alarm"));
        }
        if (location.containsKey("extras")) {
            JsonObject extras = location.getJsonObject("extras");
            for (Map.Entry<String, JsonValue> extraEntry : extras.entrySet()) {
                if (extraEntry.getKey().equals("alarm") && location.containsKey("alarm")) {
                    continue;
                }
                switch (extraEntry.getValue().getValueType()) {
                    case NUMBER -> {
                        JsonNumber jsonNumber = (JsonNumber) extraEntry.getValue();
                        if (jsonNumber.isIntegral()) {
                            position.set(extraEntry.getKey(), jsonNumber.longValue());
                        } else {
                            position.set(extraEntry.getKey(), jsonNumber.doubleValue());
                        }
                    }
                    case TRUE -> position.set(extraEntry.getKey(), true);
                    case FALSE -> position.set(extraEntry.getKey(), false);
                    case STRING -> position.set(extraEntry.getKey(), ((JsonString) extraEntry.getValue()).getString());
                    default -> {}
                }
            }
        }

        sendResponse(channel, HttpResponseStatus.OK);
        return position;
    }

    @Override
    protected void sendQueuedCommands(Channel channel, SocketAddress remoteAddress, long deviceId) {}

}
