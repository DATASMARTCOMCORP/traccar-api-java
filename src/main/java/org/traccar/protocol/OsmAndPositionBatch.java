package org.traccar.protocol;

import jakarta.json.Json;
import jakarta.json.JsonNumber;
import jakarta.json.JsonObject;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import org.traccar.model.Position;

import java.io.StringReader;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.Date;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;

/** Offline positions use the same units and attributes as the OsmAnd form decoder. */
final class OsmAndPositionBatch {

    record Batch(String deviceId, int accepted, List<Position> positions) { }
    private record Window(long start, int count) { }
    private record Fix(Instant time, double latitude, double longitude) { }

    private static final Map<Long, Window> WINDOWS = new HashMap<>();

    private OsmAndPositionBatch() { }

    static synchronized boolean allow(long deviceId) {
        long now = System.nanoTime();
        long minute = java.util.concurrent.TimeUnit.MINUTES.toNanos(1);
        WINDOWS.entrySet().removeIf(entry -> now - entry.getValue().start() >= minute);
        Window window = WINDOWS.getOrDefault(deviceId, new Window(now, 0));
        WINDOWS.put(deviceId, new Window(window.start(), Math.min(window.count() + 1, 31)));
        return window.count() < 30;
    }

    static Batch parse(String body) {
        JsonObject root;
        try (var reader = Json.createReader(new StringReader(body))) {
            root = reader.readObject();
        }
        String deviceId = root.getString("deviceId").trim();
        var items = root.getJsonArray("positions");
        if (deviceId.isEmpty() || items == null || items.isEmpty() || items.size() > 500) {
            throw new IllegalArgumentException("Invalid batch");
        }
        List<Position> positions = new ArrayList<>();
        var seen = new HashSet<Fix>();
        for (JsonValue value : items) {
            if (!(value instanceof JsonObject item)) {
                throw new IllegalArgumentException("Invalid position");
            }
            Instant time;
            double lat;
            double lon;
            try {
                time = timestamp(item.get("timestamp"));
                lat = item.getJsonNumber("lat").doubleValue();
                lon = item.getJsonNumber("lon").doubleValue();
                if (!Double.isFinite(lat) || !Double.isFinite(lon)
                        || Math.abs(lat) > 90 || Math.abs(lon) > 180) {
                    continue;
                }
            } catch (RuntimeException e) {
                // Invalid fixes are settled so they cannot permanently block the offline queue.
                continue;
            }
            if (!seen.add(new Fix(time, lat, lon))) {
                continue;
            }
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
            positions.add(position);
        }
        positions.sort(Comparator.comparing(Position::getFixTime));
        return new Batch(deviceId, items.size(), positions);
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
