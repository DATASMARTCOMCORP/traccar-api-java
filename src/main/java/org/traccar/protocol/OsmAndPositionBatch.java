package org.traccar.protocol;

import jakarta.json.Json;
import jakarta.json.JsonNumber;
import jakarta.json.JsonObject;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import org.traccar.database.IngestOutcome;
import org.traccar.database.IngestTicket;
import org.traccar.model.Position;

import java.io.StringReader;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.Date;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Offline positions use the same units and attributes as the OsmAnd form decoder. */
final class OsmAndPositionBatch {

    static final int MAX_ID_LENGTH = 128;

    /**
     * One element of the request. Exactly one of position, rejection and original is set: a position to
     * process, a final answer known from the item alone, or an earlier item of the batch with the same id.
     */
    record Item(int index, String id, Position position, IngestOutcome rejection, Item original) { }

    record Batch(String deviceId, List<Item> items) {

        /** Positions to process, in fix time order. */
        List<Position> positions() {
            return items.stream()
                    .filter(item -> item.position() != null)
                    .map(Item::position)
                    .sorted(Comparator.comparing(Position::getFixTime))
                    .toList();
        }
    }

    private record Window(long start, int count) { }

    private static final Map<Long, Window> WINDOWS = new HashMap<>();

    private OsmAndPositionBatch() { }

    static synchronized boolean allow(long deviceId, int limit) {
        if (limit <= 0) {
            return true;
        }
        long now = System.nanoTime();
        long minute = java.util.concurrent.TimeUnit.MINUTES.toNanos(1);
        WINDOWS.entrySet().removeIf(entry -> now - entry.getValue().start() >= minute);
        Window window = WINDOWS.getOrDefault(deviceId, new Window(now, 0));
        WINDOWS.put(deviceId, new Window(window.start(), Math.min(window.count() + 1, limit + 1)));
        return window.count() < limit;
    }

    /**
     * Parses a batch. Fixes older than {@code oldest} or newer than {@code newest} are rejected, as are items
     * without a usable time, coordinates or id; those answers are final so they never block the sender's queue.
     */
    static Batch parse(String body, Instant oldest, Instant newest) {
        JsonObject root;
        try (var reader = Json.createReader(new StringReader(body))) {
            root = reader.readObject();
        }
        String deviceId = root.getString("deviceId").trim();
        var values = root.getJsonArray("positions");
        if (deviceId.isEmpty() || values == null || values.isEmpty() || values.size() > 500) {
            throw new IllegalArgumentException("Invalid batch");
        }
        List<Item> items = new ArrayList<>();
        Map<String, Item> seen = new HashMap<>();
        for (int index = 0; index < values.size(); index++) {
            if (!(values.get(index) instanceof JsonObject item)) {
                items.add(rejected(index, null, "invalid"));
                continue;
            }
            Instant time;
            double lat;
            double lon;
            try {
                time = timestamp(item.get("timestamp"));
                lat = item.getJsonNumber("lat").doubleValue();
                lon = item.getJsonNumber("lon").doubleValue();
            } catch (RuntimeException e) {
                items.add(rejected(index, null, "invalid"));
                continue;
            }
            if (!Double.isFinite(lat) || !Double.isFinite(lon) || Math.abs(lat) > 90 || Math.abs(lon) > 180) {
                items.add(rejected(index, null, "invalid"));
                continue;
            }
            String id = id(item.get("id"), time, lat, lon);
            if (id == null) {
                items.add(rejected(index, null, "invalid id"));
                continue;
            }
            if (time.isBefore(oldest)) {
                items.add(rejected(index, id, "too old"));
                continue;
            }
            if (time.isAfter(newest)) {
                items.add(rejected(index, id, "future"));
                continue;
            }
            Item original = seen.get(id);
            if (original != null) {
                items.add(new Item(index, id, null, null, original));
                continue;
            }
            Item parsed = new Item(index, id, position(root, item, time, lat, lon, id), null, null);
            seen.put(id, parsed);
            items.add(parsed);
        }
        return new Batch(deviceId, items);
    }

    private static Item rejected(int index, String id, String reason) {
        return new Item(index, id, null, IngestOutcome.rejected(reason), null);
    }

    private static String id(JsonValue value, Instant time, double lat, double lon) {
        if (value == null || value == JsonValue.NULL) {
            return IngestTicket.fixId(time.toEpochMilli(), lat, lon);
        }
        String id;
        if (value instanceof JsonString string) {
            id = string.getString().trim();
        } else if (value instanceof JsonNumber number && number.isIntegral()) {
            id = number.bigIntegerValue().toString();
        } else {
            return null;
        }
        return id.isEmpty() || id.length() > MAX_ID_LENGTH ? null : id;
    }

    private static Position position(
            JsonObject root, JsonObject item, Instant time, double lat, double lon, String id) {
        Position position = new Position();
        position.setValid(true);
        position.setTime(Date.from(time));
        position.setLatitude(lat);
        position.setLongitude(lon);
        position.set(Position.KEY_ODOMETER, 0.0);
        number(item, "speed", true, position::setSpeed);
        number(item, "heading", true, position::setCourse);
        number(item, "accuracy", true, position::setAccuracy);
        number(item, "altitude", false, position::setAltitude);
        number(item, "batt", false, number -> position.set(Position.KEY_BATTERY_LEVEL, number));
        for (String key : List.of("charge", "is_moving", "heartbeat")) {
            if (item.get(key) == JsonValue.TRUE || item.get(key) == JsonValue.FALSE) {
                if (!key.equals("heartbeat") || item.getBoolean(key)) {
                    position.set(key, item.getBoolean(key));
                }
            }
        }
        text(item, "activity", position);
        text(root, "installId", position);
        text(root, "os", position);
        position.setIngestTicket(new IngestTicket(id));
        return position;
    }

    private static void text(JsonObject object, String key, Position position) {
        if (object.get(key) instanceof JsonString value && !value.getString().isBlank()) {
            position.set(key, value.getString().trim());
        }
    }

    private static void number(
            JsonObject object, String key, boolean nonNegative, java.util.function.DoubleConsumer consumer) {
        if (object.get(key) instanceof JsonNumber value) {
            double number = value.doubleValue();
            if (Double.isFinite(number) && (!nonNegative || number >= 0)) {
                consumer.accept(number);
            }
        }
    }

    static Instant timestamp(JsonValue value) {
        String text = value instanceof JsonString string ? string.getString() : value.toString();
        try {
            long epoch = new java.math.BigDecimal(text).longValueExact();
            if (epoch <= 0) {
                throw new IllegalArgumentException("Invalid epoch");
            }
            return epoch < Integer.MAX_VALUE ? Instant.ofEpochSecond(epoch) : Instant.ofEpochMilli(epoch);
        } catch (NumberFormatException e) {
            try {
                return Instant.parse(text);
            } catch (java.time.format.DateTimeParseException ex) {
                return LocalDateTime.parse(text).toInstant(ZoneOffset.UTC);
            }
        }
    }
}
