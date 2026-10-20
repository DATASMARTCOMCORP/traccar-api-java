package org.traccar.integration;

import com.google.inject.Guice;
import com.google.inject.Injector;
import com.zaxxer.hikari.HikariDataSource;
import org.traccar.Main;
import org.traccar.MainModule;
import org.traccar.TrackerConnector;
import org.traccar.model.Device;
import org.traccar.model.Event;
import org.traccar.model.LogRecord;
import org.traccar.model.Position;
import org.traccar.protocol.OsmAndProtocol;
import org.traccar.session.ConnectionManager;
import org.traccar.storage.DatabaseModule;
import org.traccar.storage.Storage;
import org.traccar.web.WebModule;

import javax.sql.DataSource;
import java.io.IOException;
import java.net.ServerSocket;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.ExecutorService;
import java.util.stream.Collectors;

/**
 * A complete Traccar server (OsmAnd listener, processing pipeline, storage, notifications) in this JVM. Several
 * instances can share one database, like servers behind a load balancer.
 */
final class TraccarInstance implements AutoCloseable {

    private static final HttpClient CLIENT = HttpClient.newHttpClient();

    private final Injector injector;
    private final java.util.Collection<TrackerConnector> connectors;
    private final int port;

    /** Positions and events this instance pushed to WebSocket clients of the watched user. */
    final List<Position> socketPositions = new CopyOnWriteArrayList<>();
    final List<Event> socketEvents = new CopyOnWriteArrayList<>();

    TraccarInstance(Path directory, String jdbcUrl, String forwardUrl, Map<String, String> extra) throws Exception {
        port = freePort();
        var config = new java.util.TreeMap<String, String>();
        config.put("database.driver", "org.postgresql.Driver");
        config.put("database.url", jdbcUrl);
        config.put("database.user", "postgres");
        config.put("database.password", "postgres");
        config.put("database.changelog", Path.of("schema/changelog-master.xml").toAbsolutePath().toString());
        config.put("protocols.enable", "osmand");
        config.put("osmand.port", String.valueOf(port));
        config.put("osmand.address", "127.0.0.1");
        config.put("server.statistics", "");
        config.put("server.buffering.threshold", "0");
        config.put("geocoder.enable", "false");
        config.put("notificator.types", "web");
        config.put("web.path", directory.toString());
        config.put("logger.console", "true");
        config.put("logger.level", "warning");
        if (forwardUrl != null) {
            config.put("event.forward.url", forwardUrl);
        }
        config.putAll(extra);
        Path file = Files.createTempFile(directory, "traccar", ".xml");
        Files.writeString(file, "<?xml version='1.0' encoding='UTF-8'?>\n"
                + "<!DOCTYPE properties SYSTEM 'http://java.sun.com/dtd/properties.dtd'>\n<properties>\n"
                + config.entrySet().stream()
                        .map(e -> "<entry key='" + e.getKey() + "'>" + e.getValue() + "</entry>")
                        .collect(Collectors.joining("\n"))
                + "\n</properties>\n");
        injector = Guice.createInjector(new MainModule(file.toString()), new DatabaseModule(), new WebModule());
        // ServerManager finds protocols by scanning the package directory, which on the test class path is the
        // test one; start the OsmAnd connectors the same way it would. Netty pipelines take the injector from
        // Main while they are built, so point it at this instance first.
        synchronized (TraccarInstance.class) {
            var field = Main.class.getDeclaredField("injector");
            field.setAccessible(true);
            field.set(null, injector);
            connectors = injector.getInstance(OsmAndProtocol.class).getConnectorList();
        }
        for (var connector : connectors) {
            connector.start();
        }
    }

    int port() {
        return port;
    }

    <T> T get(Class<T> type) {
        return injector.getInstance(type);
    }

    Storage storage() {
        return injector.getInstance(Storage.class);
    }

    void watch(long userId) throws Exception {
        injector.getInstance(ConnectionManager.class).addListener(userId, new ConnectionManager.UpdateListener() {
            @Override
            public void onKeepalive() {
            }

            @Override
            public void onUpdateDevice(Device device) {
            }

            @Override
            public void onUpdatePosition(Position position) {
                socketPositions.add(position);
            }

            @Override
            public void onUpdateEvent(Event event) {
                socketEvents.add(event);
            }

            @Override
            public void onUpdateLog(LogRecord record) {
            }
        });
    }

    HttpResponse<String> live(String query) throws Exception {
        return CLIENT.send(HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/"))
                .header("Content-Type", "application/x-www-form-urlencoded")
                .POST(HttpRequest.BodyPublishers.ofString(query, StandardCharsets.UTF_8))
                .build(), HttpResponse.BodyHandlers.ofString());
    }

    HttpResponse<String> batch(String json) throws Exception {
        return CLIENT.send(HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/positions/batch"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(json, StandardCharsets.UTF_8))
                .build(), HttpResponse.BodyHandlers.ofString());
    }

    private static int freePort() throws IOException {
        try (var socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    @Override
    public void close() throws Exception {
        for (var connector : connectors) {
            connector.stop();
        }
        injector.getInstance(ExecutorService.class).shutdownNow();
        DataSource dataSource = injector.getInstance(DataSource.class);
        if (dataSource instanceof HikariDataSource hikari) {
            hikari.close();
        }
    }

}
