package org.traccar.integration;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import io.zonky.test.db.postgres.embedded.EmbeddedPostgres;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.io.TempDir;
import org.traccar.model.Device;
import org.traccar.model.Geofence;
import org.traccar.model.Notification;
import org.traccar.model.Permission;
import org.traccar.model.User;
import org.traccar.storage.Storage;
import org.traccar.storage.query.Columns;
import org.traccar.storage.query.Request;

import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Runs Traccar servers against a real Postgres database (embedded binaries, schema from the Liquibase
 * changelog), feeds them over HTTP like the mobile clients and the queue consumer do, and reads the outcome
 * from the tables, the WebSocket listeners and the event webhook.
 */
abstract class IngestionTestSupport {

    static final ObjectMapper JSON = new ObjectMapper();

    /** Geofence center; the route below crosses it with exactly one point inside. */
    static final double CENTER_LAT = -22.900;
    static final double CENTER_LON = -43.200;
    static final double STEP = 0.003; // about 333 m, more than the 300 m radius

    private static final AtomicInteger DATABASES = new AtomicInteger();
    private static EmbeddedPostgres postgres;

    @TempDir
    Path directory;

    String jdbcUrl;
    HttpServer webhook;
    final List<JsonNode> forwarded = new CopyOnWriteArrayList<>();
    final List<JsonNode> forwardedPositions = new CopyOnWriteArrayList<>();
    /** What the webhook answers; deliveries are recorded only when it accepts them. */
    volatile int webhookStatus = 200;
    private final Semaphore forwardedPermits = new Semaphore(0);
    private int forwardedSeen;
    final List<TraccarInstance> instances = new ArrayList<>();

    /** Fix times are recent so notifications are not skipped as old (notificator.timeThreshold). */
    final Instant base = Instant.now().truncatedTo(ChronoUnit.SECONDS).minus(Duration.ofMinutes(10));

    record Fixture(long userId, long deviceId, String uniqueId, long geofenceId) { }

    private static synchronized String createDatabase() throws Exception {
        if (postgres == null) {
            postgres = EmbeddedPostgres.builder().start();
            Runtime.getRuntime().addShutdownHook(new Thread(() -> {
                try {
                    postgres.close();
                } catch (Exception ignored) {
                    // best effort
                }
            }));
            execute("CREATE DATABASE ingest_template");
            // The first server on a database runs the Liquibase changelog; every test copies the result
            try (var ignored = new TraccarInstance(
                    java.nio.file.Files.createTempDirectory("traccar"), url("ingest_template"), null, Map.of())) {
                // migrated
            }
        }
        String name = "ingest_" + DATABASES.incrementAndGet();
        execute("CREATE DATABASE " + name + " TEMPLATE ingest_template");
        return url(name);
    }

    private static String url(String database) {
        return "jdbc:postgresql://localhost:" + postgres.getPort() + "/" + database;
    }

    private static void execute(String sql) throws SQLException {
        try (Connection connection = DriverManager.getConnection(url("postgres"), "postgres", "postgres")) {
            connection.createStatement().execute(sql);
        }
    }

    @BeforeEach
    void setUpDatabase() throws Exception {
        jdbcUrl = createDatabase();
        webhook = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        webhook.createContext("/events", exchange -> {
            int status = webhookStatus;
            JsonNode body = JSON.readTree(exchange.getRequestBody());
            exchange.sendResponseHeaders(status, -1);
            exchange.close();
            if (status == 200) {
                forwarded.add(body);
                forwardedPermits.release();
            }
        });
        webhook.createContext("/positions", exchange -> {
            forwardedPositions.add(JSON.readTree(exchange.getRequestBody()));
            exchange.sendResponseHeaders(200, -1);
            exchange.close();
        });
        webhook.start();
    }

    @AfterEach
    void tearDown() throws Exception {
        for (var instance : instances) {
            instance.close();
        }
        webhook.stop(0);
    }

    Map<String, String> config() {
        return Map.of(
                "ingest.batchesPerMinute", "0",
                "filter.enable", "true",
                "filter.zero", "true",
                "report.trip.minDistance", "200",
                "report.trip.minDuration", "60");
    }

    /** A server configured like production (positions acknowledged before processing, no buffering). */
    TraccarInstance start() throws Exception {
        return start(Map.of());
    }

    /**
     * A server that holds single-request replies until the position is processed
     * (server.delayAcknowledgement), so a test can read the outcome as soon as the reply arrives.
     */
    TraccarInstance startSynchronous() throws Exception {
        return start(Map.of("server.delayAcknowledgement", "true"));
    }

    TraccarInstance start(Map<String, String> overrides) throws Exception {
        var settings = new HashMap<>(config());
        settings.putAll(overrides);
        var instance = new TraccarInstance(directory, jdbcUrl,
                "http://127.0.0.1:" + webhook.getAddress().getPort() + "/events", settings);
        instances.add(instance);
        return instance;
    }

    Fixture seed(TraccarInstance instance, String uniqueId, Map<String, Object> deviceAttributes) throws Exception {
        Storage storage = instance.storage();
        Request insert = new Request(new Columns.Exclude("id"));

        User user = new User();
        user.setName("watcher-" + uniqueId);
        user.setEmail(uniqueId + "@example.com");
        user.setId(storage.addObject(user, insert));

        Device device = new Device();
        device.setName(uniqueId);
        device.setUniqueId(uniqueId);
        deviceAttributes.forEach((key, value) -> device.getAttributes().put(key, value));
        device.setId(storage.addObject(device, insert));

        Geofence geofence = new Geofence();
        geofence.setName("port-" + uniqueId);
        geofence.setArea("CIRCLE (" + CENTER_LAT + " " + CENTER_LON + ", 300)");
        geofence.setId(storage.addObject(geofence, insert));

        storage.addPermission(new Permission(User.class, user.getId(), Device.class, device.getId()));
        storage.addPermission(new Permission(Device.class, device.getId(), Geofence.class, geofence.getId()));
        for (String type : List.of("geofenceEnter", "geofenceExit", "deviceOverspeed", "deviceMoving",
                "deviceStopped")) {
            Notification notification = new Notification();
            notification.setType(type);
            notification.setNotificators("web");
            notification.setAlways(false);
            notification.setId(storage.addObject(notification, insert));
            storage.addPermission(new Permission(User.class, user.getId(), Notification.class, notification.getId()));
            storage.addPermission(new Permission(
                    Device.class, device.getId(), Notification.class, notification.getId()));
        }
        return new Fixture(user.getId(), device.getId(), uniqueId, geofence.getId());
    }

    // Route: point k is k steps north of the start; point 10 is the geofence center.
    static double routeLat(int k) {
        return CENTER_LAT - 10 * STEP + k * STEP;
    }

    Instant time(int k) {
        return base.plusSeconds(20L * k);
    }

    String liveQuery(Fixture fixture, int k, double speed) {
        return "id=" + fixture.uniqueId() + "&lat=" + routeLat(k) + "&lon=" + CENTER_LON
                + "&timestamp=" + time(k) + "&speed=" + speed + "&batt=80";
    }

    static String batchItem(Instant time, double lat, double lon, double speed, String id) {
        return "{" + (id != null ? "\"id\":\"" + id + "\"," : "")
                + "\"timestamp\":\"" + time + "\",\"lat\":" + lat + ",\"lon\":" + lon + ",\"speed\":" + speed + "}";
    }

    String routeItem(int k, double speed) {
        return batchItem(time(k), routeLat(k), CENTER_LON, speed, null);
    }

    static String batch(Fixture fixture, List<String> items) {
        return "{\"deviceId\":\"" + fixture.uniqueId() + "\",\"os\":\"android\",\"positions\":["
                + String.join(",", items) + "]}";
    }

    // Observations

    record StoredRow(long id, Instant fixTime, double latitude, double totalDistance, double distance) { }

    record EventRow(String type, long positionId, long geofenceId, boolean late) { }

    Connection connect() throws SQLException {
        return DriverManager.getConnection(jdbcUrl, "postgres", "postgres");
    }

    List<StoredRow> positions(long deviceId) throws Exception {
        var result = new ArrayList<StoredRow>();
        try (var connection = connect(); var statement = connection.prepareStatement(
                "SELECT id, fixtime, latitude, attributes FROM tc_positions WHERE deviceid = ? ORDER BY fixtime, id")) {
            statement.setLong(1, deviceId);
            try (var rs = statement.executeQuery()) {
                while (rs.next()) {
                    JsonNode attributes = JSON.readTree(rs.getString(4));
                    result.add(new StoredRow(rs.getLong(1), rs.getTimestamp(2).toInstant(), rs.getDouble(3),
                            attributes.path("totalDistance").asDouble(), attributes.path("distance").asDouble()));
                }
            }
        }
        return result;
    }

    long latestPositionId(long deviceId) throws Exception {
        try (var connection = connect(); var statement = connection.prepareStatement(
                "SELECT positionid FROM tc_devices WHERE id = ?")) {
            statement.setLong(1, deviceId);
            try (var rs = statement.executeQuery()) {
                rs.next();
                return rs.getLong(1);
            }
        }
    }

    Instant fixTime(long positionId) throws Exception {
        try (var connection = connect(); var statement = connection.prepareStatement(
                "SELECT fixtime FROM tc_positions WHERE id = ?")) {
            statement.setLong(1, positionId);
            try (var rs = statement.executeQuery()) {
                return rs.next() ? rs.getTimestamp(1).toInstant() : null;
            }
        }
    }

    List<EventRow> events(long deviceId) throws Exception {
        var result = new ArrayList<EventRow>();
        try (var connection = connect(); var statement = connection.prepareStatement(
                "SELECT type, positionid, geofenceid, attributes FROM tc_events WHERE deviceid = ? ORDER BY id")) {
            statement.setLong(1, deviceId);
            try (var rs = statement.executeQuery()) {
                while (rs.next()) {
                    result.add(new EventRow(rs.getString(1), rs.getLong(2), rs.getLong(3),
                            JSON.readTree(rs.getString(4)).path("late").asBoolean()));
                }
            }
        }
        return result;
    }

    /** Events of the given types, each as "type@latitude" of its position, in storage order. */
    List<String> eventTrail(long deviceId, String... types) throws Exception {
        var latitudes = new HashMap<Long, Double>();
        for (var row : positions(deviceId)) {
            latitudes.put(row.id(), row.latitude());
        }
        var wanted = List.of(types);
        return events(deviceId).stream()
                .filter(event -> wanted.contains(event.type()))
                .map(event -> event.type() + "@" + latitudes.get(event.positionId()))
                .toList();
    }

    /** Waits for the asynchronous webhook calls; the count to expect comes from tc_events. */
    void awaitForwarded(int total) throws InterruptedException {
        int missing = total - forwardedSeen;
        if (missing > 0) {
            if (!forwardedPermits.tryAcquire(missing, 10, TimeUnit.SECONDS)) {
                throw new AssertionError("expected " + total + " forwarded events, got " + forwarded.size());
            }
            forwardedSeen = total;
        }
    }

    /** Waits for a condition that asynchronous deliveries make true; fails after 10 seconds. */
    static void await(java.util.function.BooleanSupplier condition, String description) {
        long deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos();
        while (!condition.getAsBoolean()) {
            if (System.nanoTime() > deadline) {
                throw new AssertionError("timed out waiting for " + description);
            }
            Thread.onSpinWait();
        }
    }

    String positionsUrl() {
        return "http://127.0.0.1:" + webhook.getAddress().getPort() + "/positions";
    }

    static JsonNode body(java.net.http.HttpResponse<String> response) throws Exception {
        return JSON.readTree(response.body().getBytes(StandardCharsets.UTF_8));
    }

}
