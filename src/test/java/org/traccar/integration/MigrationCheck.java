package org.traccar.integration;

import liquibase.Contexts;
import liquibase.LabelExpression;
import liquibase.Liquibase;
import liquibase.changelog.ChangeSet;
import liquibase.changelog.DatabaseChangeLog;
import liquibase.changelog.visitor.AbstractChangeExecListener;
import liquibase.changelog.visitor.ChangeExecListener;
import liquibase.database.Database;
import liquibase.database.DatabaseFactory;
import liquibase.database.jvm.JdbcConnection;
import liquibase.resource.DirectoryResourceAccessor;

import java.io.IOException;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.stream.Collectors;

/**
 * Validates the pending Liquibase changesets against a disposable PostgreSQL shaped like production: the
 * production changelog history (everything through 6.13.0 and changelog-6.13-dsc, without
 * 6.13.0-fk-linkeddeviceid and 6.15.0, no TimescaleDB) and production-sized synthetic data. It measures each
 * pending changeset, the locks it takes and how long a concurrent writer is blocked, checks that a second run
 * changes nothing, records the checksums to compare with production, and finally starts the server on the
 * migrated database and ingests a batch.
 *
 * <p>Never point it at a real database: it creates and fills its own cluster. Run with
 * {@code ./gradlew migrationCheck -PpgBin=/path/to/postgresql-16/bin [-Ppositions=17600000] [-Pactions=500000]}.
 */
public final class MigrationCheck {

    private static final List<String> PRODUCTION_FILES = List.of(
            "changelog-4.0-clean", "changelog-4.1", "changelog-4.7", "changelog-4.9", "changelog-4.10",
            "changelog-4.11", "changelog-4.13", "changelog-4.15", "changelog-5.0", "changelog-5.1", "changelog-5.2",
            "changelog-5.3", "changelog-5.4", "changelog-5.5", "changelog-5.6", "changelog-5.7", "changelog-5.8",
            "changelog-5.9", "changelog-5.10", "changelog-5.11", "changelog-6.2", "changelog-6.3", "changelog-6.6",
            "changelog-6.7.0", "changelog-6.8.0", "changelog-6.9.0", "changelog-6.11.0", "changelog-6.13.0",
            "changelog-6.13-dsc");

    private MigrationCheck() {
    }

    private static void log(String format, Object... args) {
        System.out.printf("[%s] %s%n", Instant.now().toString().substring(11, 19), String.format(format, args));
    }

    public static void main(String[] args) throws Exception {
        Path pgBin = Path.of(System.getProperty("pgBin"));
        long positions = Long.getLong("positions", 17_600_000L);
        long actions = Long.getLong("actions", 500_000L);
        Path work = Files.createTempDirectory("migration-check");
        int port;
        try (var socket = new ServerSocket(0)) {
            port = socket.getLocalPort();
        }
        Path data = work.resolve("data");
        run(pgBin.resolve("initdb").toString(), "-D", data.toString(), "-U", "postgres", "-A", "trust",
                "-E", "UTF8", "--locale=C");
        run(pgBin.resolve("pg_ctl").toString(), "-D", data.toString(), "-l", work.resolve("pg.log").toString(),
                "-o", "-p " + port + " -h 127.0.0.1 -c unix_socket_directories='' -c timezone=America/Sao_Paulo"
                        + " -c max_wal_size=4GB -c shared_buffers=512MB", "-w", "start");
        String admin = "jdbc:postgresql://127.0.0.1:" + port + "/postgres";
        String url = "jdbc:postgresql://127.0.0.1:" + port + "/traccar_prd_shape";
        try {
            try (Connection connection = DriverManager.getConnection(admin, "postgres", "")) {
                log("PostgreSQL %s", scalar(connection, "SHOW server_version"));
                connection.createStatement().execute("CREATE DATABASE traccar_prd_shape");
            }
            Path schema = Path.of("schema").toAbsolutePath();
            Path productionSchema = productionChangelog(schema, work);
            try (Connection connection = DriverManager.getConnection(url, "postgres", "")) {
                log("Building the production schema history");
                update(DriverManager.getConnection(url, "postgres", ""), productionSchema, null);
                // Production never ran this one (it lives in the same file as 6.13.0)
                connection.createStatement().execute(
                        "ALTER TABLE tc_device_device DROP CONSTRAINT fk_device_device_linkeddeviceid");
                connection.createStatement().execute(
                        "DELETE FROM databasechangelog WHERE id = 'changelog-6.13.0-fk-linkeddeviceid'");
                log("History: %s rows, TimescaleDB installed: %s",
                        scalar(connection, "SELECT count(*) FROM databasechangelog"),
                        scalar(connection, "SELECT count(*) FROM pg_extension WHERE extname = 'timescaledb'"));
                fill(connection, positions, actions);
                connection.createStatement().execute("VACUUM ANALYZE");
                log("Table sizes: %s", scalar(connection, "SELECT string_agg(relname || '=' || n_live_tup"
                        + " || ' rows/' || pg_size_pretty(pg_total_relation_size(relid)), ', ' ORDER BY relname)"
                        + " FROM pg_stat_user_tables WHERE relname IN ('tc_positions', 'tc_events', 'tc_actions',"
                        + " 'tc_devices', 'tc_geofences', 'tc_device_geofence')"));

                var pending = pending(DriverManager.getConnection(url, "postgres", ""), schema);
                log("Pending changesets: %s", pending);

                String before = snapshot(connection);
                migrate(DriverManager.getConnection(url, "postgres", ""), url, schema);
                String after = snapshot(connection);
                log("Schema objects changed by the migration: %s", difference(before, after));

                log("Second run");
                var second = update(
                        DriverManager.getConnection(url, "postgres", ""), schema.resolve("changelog-master.xml"), null);
                String again = snapshot(connection);
                log("Second run executed %d changesets; schema identical: %s", second.size(), again.equals(after));

                log("Checksums to compare with production (SELECT id, md5sum FROM databasechangelog):");
                try (var rs = connection.createStatement().executeQuery(
                        "SELECT id, md5sum FROM databasechangelog ORDER BY orderexecuted")) {
                    while (rs.next()) {
                        System.out.println("    " + rs.getString(1) + "\t" + rs.getString(2));
                    }
                }
            }
            startAndIngest(work, url);
        } finally {
            run(pgBin.resolve("pg_ctl").toString(), "-D", data.toString(), "-m", "fast", "-w", "stop");
            log("Cluster stopped; data left in %s (delete it when done)", work);
        }
        System.exit(0); // the in-process server leaves non-daemon threads behind
    }

    private static Path productionChangelog(Path schema, Path work) throws IOException {
        Path copy = work.resolve("schema-production");
        Files.createDirectories(copy);
        try (var files = Files.list(schema)) {
            for (Path file : files.toList()) {
                Files.copy(file, copy.resolve(file.getFileName()));
            }
        }
        String includes = PRODUCTION_FILES.stream()
                .map(name -> "  <include file=\"" + name + ".xml\" relativeToChangelogFile=\"true\" />")
                .collect(Collectors.joining("\n"));
        Files.writeString(copy.resolve("changelog-production.xml"),
                "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<databaseChangeLog"
                        + " xmlns=\"http://www.liquibase.org/xml/ns/dbchangelog\""
                        + " xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\""
                        + " xsi:schemaLocation=\"http://www.liquibase.org/xml/ns/dbchangelog"
                        + " http://www.liquibase.org/xml/ns/dbchangelog/dbchangelog-3.4.xsd\">\n"
                        + includes + "\n</databaseChangeLog>\n");
        return copy.resolve("changelog-production.xml");
    }

    /** Synthetic data with production's counts (inventory 2026-10-15) and value shapes, no real data. */
    private static void fill(Connection connection, long positions, long actions) throws SQLException {
        var statement = connection.createStatement();
        log("Filling: 5 users, 26 groups, 1,038 devices, 2,253 geofences (212 above 4,096 characters),"
                + " 21,491 links, %,d positions, 556,000 events, %,d actions", positions, actions);
        statement.execute("INSERT INTO tc_users (name, email, hashedpassword, salt, administrator, attributes)"
                + " SELECT 'user' || g, 'user' || g || '@example.com', 'x', 'x', g < 5, '{}'"
                + " FROM generate_series(1, 5) g");
        statement.execute("INSERT INTO tc_groups (name, attributes) SELECT 'group' || g, '{}'"
                + " FROM generate_series(1, 26) g");
        statement.execute("INSERT INTO tc_devices (name, uniqueid, attributes, groupid)"
                + " SELECT 'device' || g, 'dev-' || g, '{\"certificado\":\"x\"}', 1 + g % 26"
                + " FROM generate_series(1, 1038) g");
        statement.execute("INSERT INTO tc_geofences (name, area, attributes)"
                + " SELECT 'fence' || g, CASE WHEN g <= 212 THEN 'POLYGON((' || (SELECT string_agg("
                + "   (-22.9 + 0.001 * sin(i)) || ' ' || (-43.2 + 0.001 * cos(i)), ', ')"
                + "   FROM generate_series(1, 120 + g * 40) i) || '))'"
                + " ELSE 'CIRCLE (' || (-22.9 + g * 0.0001) || ' -43.2, 300)' END, '{}'"
                + " FROM generate_series(1, 2253) g");
        statement.execute("INSERT INTO tc_device_geofence (deviceid, geofenceid)"
                + " SELECT 1 + (g % 1038), 1 + (g * 7 % 2253) FROM generate_series(1, 21491) g"
                + " ON CONFLICT DO NOTHING");
        long chunk = 2_000_000;
        for (long from = 1; from <= positions; from += chunk) {
            long to = Math.min(positions, from + chunk - 1);
            statement.execute("INSERT INTO tc_positions (protocol, deviceid, servertime, devicetime, fixtime,"
                    + " valid, latitude, longitude, altitude, speed, course, address, attributes, accuracy, network,"
                    + " geofenceids)"
                    + " SELECT 'osmand', 1 + g % 1038, ts, ts, ts, true, -22.9 + (g % 1000) * 0.0001,"
                    + " -43.2 + (g % 997) * 0.0001, 10, (g % 40)::float, (g % 360)::float, NULL,"
                    + " '{\"batteryLevel\":80.0,\"distance\":12.5,\"totalDistance\":' || (g * 3) || '.0,"
                    + "\"motion\":true,\"is_moving\":true,\"activity\":\"in_vehicle\",\"odometer\":0.0}', 5, NULL,"
                    + " CASE WHEN g % 50 = 0 THEN '[' || (1 + g % 2253) || ']' END"
                    + " FROM generate_series(" + from + ", " + to + ") g,"
                    + " LATERAL (SELECT timestamp '2026-01-01' + (g * interval '1 second') / 3 AS ts) t");
            log("  positions %,d / %,d", to, positions);
        }
        statement.execute("UPDATE tc_devices d SET positionid = p.id FROM (SELECT deviceid, max(id) AS id"
                + " FROM tc_positions GROUP BY deviceid) p WHERE p.deviceid = d.id");
        statement.execute("INSERT INTO tc_events (type, eventtime, deviceid, positionid, geofenceid, attributes)"
                + " SELECT CASE g % 4 WHEN 0 THEN 'geofenceEnter' WHEN 1 THEN 'geofenceExit'"
                + " WHEN 2 THEN 'deviceMoving' ELSE 'deviceStopped' END,"
                + " timestamp '2026-01-01' + g * interval '20 seconds', 1 + g % 1038, g * 31,"
                + " CASE WHEN g % 4 < 2 THEN 1 + g % 2253 END, '{}' FROM generate_series(1, 556000) g");
        statement.execute("INSERT INTO tc_actions (actiontime, address, userid, actiontype, objecttype, objectid,"
                + " attributes) SELECT timestamp '2025-01-01' + g * interval '1 minute', '10.0.0.1', 1 + g % 5,"
                + " CASE WHEN g % 3 = 0 THEN 'login' ELSE 'edit' END, 'geofence', 1 + g % 2253, '{}'"
                + " FROM generate_series(1, " + actions + ") g");
    }

    private static List<String> pending(Connection connection, Path schema) throws Exception {
        Database database = database(connection);
        try (var liquibase = new Liquibase("changelog-master.xml", new DirectoryResourceAccessor(schema),
                database)) {
            return liquibase.listUnrunChangeSets(new Contexts(), new LabelExpression()).stream()
                    .map(ChangeSet::getId).toList();
        }
    }

    private static Database database(Connection connection) throws Exception {
        return DatabaseFactory.getInstance().findCorrectDatabaseImplementation(new JdbcConnection(connection));
    }

    /**
     * Runs a changelog on its own connection, which Liquibase closes; returns how long each executed changeset
     * took, in milliseconds.
     */
    private static Map<String, Long> update(Connection connection, Path changelog, ChangeExecListener extra)
            throws Exception {
        var timings = new LinkedHashMap<String, Long>();
        var started = new ConcurrentHashMap<String, Long>();
        Database database = database(connection);
        try (var liquibase = new Liquibase(changelog.getFileName().toString(),
                new DirectoryResourceAccessor(changelog.getParent()), database)) {
            liquibase.setChangeExecListener(new AbstractChangeExecListener() {
                @Override
                public void willRun(ChangeSet changeSet, DatabaseChangeLog log, Database db,
                        ChangeSet.RunStatus status) {
                    started.put(changeSet.getId(), System.nanoTime());
                    if (extra != null) {
                        extra.willRun(changeSet, log, db, status);
                    }
                }

                @Override
                public void ran(ChangeSet changeSet, DatabaseChangeLog log, Database db,
                        ChangeSet.ExecType type) {
                    Long start = started.get(changeSet.getId());
                    timings.put(changeSet.getId() + " (" + type + ")",
                            start != null ? (System.nanoTime() - start) / 1_000_000 : -1);
                    if (extra != null) {
                        extra.ran(changeSet, log, db, type);
                    }
                }
            });
            liquibase.update(new Contexts(), new LabelExpression());
        }
        return timings;
    }

    /**
     * Runs the pending changesets while a probe writes like the server does (insert a position, move the
     * device's latest position, insert an action) every 10 ms, and a sampler records the locks the migration
     * session holds.
     */
    private static void migrate(Connection connection, String url, Path schema) throws Exception {
        int migrationPid = Integer.parseInt(scalar(connection, "SELECT pg_backend_pid()"));
        var stop = new AtomicBoolean();
        var maxWrite = new AtomicLong();
        var slowWrites = new AtomicLong();
        var writes = new AtomicLong();
        Set<String> locks = new TreeSet<>();
        Thread probe = new Thread(() -> {
            try (Connection writer = DriverManager.getConnection(url, "postgres", "")) {
                var insert = writer.prepareStatement("INSERT INTO tc_positions (protocol, deviceid, servertime,"
                        + " devicetime, fixtime, valid, latitude, longitude, altitude, speed, course, attributes)"
                        + " VALUES ('osmand', 7, now(), now(), now(), true, -22.9, -43.2, 0, 0, 0, '{}')"
                        + " RETURNING id");
                var latest = writer.prepareStatement("UPDATE tc_devices SET positionid = ? WHERE id = 7");
                var action = writer.prepareStatement("INSERT INTO tc_actions (actiontime, address, userid,"
                        + " actiontype, objecttype, objectid, attributes)"
                        + " VALUES (now(), '10.0.0.1', 1, 'login', 'user', 1, '{}')");
                while (!stop.get()) {
                    long start = System.nanoTime();
                    try (var rs = insert.executeQuery()) {
                        rs.next();
                        latest.setLong(1, rs.getLong(1));
                    }
                    latest.executeUpdate();
                    action.executeUpdate();
                    long took = (System.nanoTime() - start) / 1_000_000;
                    maxWrite.accumulateAndGet(took, Math::max);
                    writes.incrementAndGet();
                    if (took > 100) {
                        slowWrites.incrementAndGet();
                    }
                    Thread.sleep(10);
                }
            } catch (Exception e) {
                log("probe failed: %s", e);
            }
        });
        Thread sampler = new Thread(() -> {
            try (Connection observer = DriverManager.getConnection(url, "postgres", "")) {
                var query = observer.prepareStatement("SELECT coalesce(c.relname, l.locktype) || ':' || l.mode"
                        + " FROM pg_locks l LEFT JOIN pg_class c ON c.oid = l.relation"
                        + " WHERE l.pid = ? AND l.granted AND l.mode IN ('AccessExclusiveLock', 'ExclusiveLock',"
                        + " 'ShareRowExclusiveLock', 'ShareLock') AND (c.relname IS NULL OR c.relname LIKE 'tc_%')");
                query.setInt(1, migrationPid);
                while (!stop.get()) {
                    try (var rs = query.executeQuery()) {
                        while (rs.next()) {
                            synchronized (locks) {
                                locks.add(rs.getString(1));
                            }
                        }
                    }
                    Thread.sleep(5);
                }
            } catch (Exception e) {
                log("sampler failed: %s", e);
            }
        });
        probe.start();
        sampler.start();
        Thread.sleep(500); // a baseline for the probe
        long baseline = maxWrite.getAndSet(0);
        long start = System.nanoTime();
        var timings = update(connection, schema.resolve("changelog-master.xml"), null);
        long total = (System.nanoTime() - start) / 1_000_000;
        Thread.sleep(500);
        stop.set(true);
        probe.join();
        sampler.join();
        log("Migration: %d ms in total", total);
        timings.forEach((id, ms) -> log("  %-48s %6d ms", id, ms));
        log("Strong locks taken by the migration session (sampled every 5 ms): %s", locks);
        log("Concurrent writer: %d write cycles, slowest %d ms (before the migration: %d ms), %d over 100 ms",
                writes.get(), maxWrite.get(), baseline, slowWrites.get());
    }

    private static String snapshot(Connection connection) throws SQLException {
        var objects = new ArrayList<String>();
        try (var rs = connection.createStatement().executeQuery(
                "SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type || ' '"
                        + " || is_nullable || ' ' || coalesce(column_default, '') FROM information_schema.columns"
                        + " WHERE table_schema = 'public' UNION ALL SELECT 'index ' || indexdef FROM pg_indexes"
                        + " WHERE schemaname = 'public' UNION ALL SELECT 'constraint ' || conrelid::regclass || ' '"
                        + " || pg_get_constraintdef(oid) FROM pg_constraint WHERE connamespace = 'public'::regnamespace"
                        + " ORDER BY 1")) {
            while (rs.next()) {
                if (!rs.getString(1).contains("databasechangelog")) {
                    objects.add(rs.getString(1));
                }
            }
        }
        return String.join("\n", objects);
    }

    private static String difference(String before, String after) {
        Set<String> old = Set.of(before.split("\n"));
        Set<String> now = Set.of(after.split("\n"));
        var added = now.stream().filter(o -> !old.contains(o)).sorted().map(o -> "\n    + " + o);
        var removed = old.stream().filter(o -> !now.contains(o)).sorted().map(o -> "\n    - " + o);
        return java.util.stream.Stream.concat(removed, added).collect(Collectors.joining());
    }

    private static void startAndIngest(Path work, String url) throws Exception {
        log("Starting the server on the migrated database");
        Path directory = Files.createDirectories(work.resolve("server"));
        try (var server = new TraccarInstance(directory, url, null, Map.of(
                "database.password", "", "ingest.batchesPerMinute", "0"))) {
            String now = Instant.now().minusSeconds(60).toString();
            var response = server.batch("{\"deviceId\":\"dev-1\",\"positions\":[{\"timestamp\":\"" + now
                    + "\",\"lat\":-22.9,\"lon\":-43.2,\"speed\":10}]}");
            log("Batch after migration: HTTP %d %s", response.statusCode(), response.body());
            try (Connection connection = DriverManager.getConnection(url, "postgres", "")) {
                log("Receipts: %s, outbox entries: %s, device dev-1 latest = new position: %s",
                        scalar(connection, "SELECT count(*) FROM tc_position_receipts"),
                        scalar(connection, "SELECT count(*) FROM tc_outbox"),
                        scalar(connection, "SELECT d.positionid = (SELECT max(id) FROM tc_positions"
                                + " WHERE deviceid = d.id) FROM tc_devices d WHERE uniqueid = 'dev-1'"));
            }
        }
    }

    private static String scalar(Connection connection, String sql) throws SQLException {
        try (var rs = connection.createStatement().executeQuery(sql)) {
            rs.next();
            return rs.getString(1);
        }
    }

    private static void run(String... command) throws IOException, InterruptedException {
        var process = new ProcessBuilder(command).redirectErrorStream(true).start();
        String output = new String(process.getInputStream().readAllBytes());
        if (process.waitFor() != 0) {
            throw new IOException(String.join(" ", command) + " failed:\n" + output);
        }
    }

}
