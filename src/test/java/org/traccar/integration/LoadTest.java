package org.traccar.integration;

import com.sun.net.httpserver.HttpServer;
import com.zaxxer.hikari.HikariDataSource;
import org.traccar.ProcessingHandler;

import javax.sql.DataSource;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReferenceArray;
import java.util.regex.Pattern;

/**
 * Load and recovery harness for batch ingestion: synthetic devices reporting every few seconds through the
 * batch endpoint (as the queue consumer does), several servers sharing one PostgreSQL, duplicates, shuffled
 * offline batches concurrent with live reports, a server killed and restarted mid-run, and a webhook outage.
 * Throughput is measured as rows committed in the database, not as HTTP replies.
 *
 * <p>Everything is local and synthetic: a disposable PostgreSQL cluster and a stub webhook. Run with
 * {@code ./gradlew loadTest -PpgBin=/path/to/postgresql-16/bin} and the -P options in docs/ingestion-validation.md.
 */
public final class LoadTest {

    private static final Pattern STATUS = Pattern.compile("\"status\":\"(\\w+)\"(?:,\"reason\":\"([^\"]*)\")?");

    // Parameters
    private final int devices = Integer.getInteger("devices", 20_000);
    private final int interval = Integer.getInteger("interval", 5);
    private final int duration = Integer.getInteger("duration", 300);
    private final int instancesCount = Integer.getInteger("instances", 2);
    private final int geofences = Integer.getInteger("geofences", 2_253);
    private final int linksPerDevice = Integer.getInteger("linksPerDevice", 20);
    private final int crossingSeconds = Integer.getInteger("crossingSeconds", 600);
    private final double offlineShare = Double.parseDouble(System.getProperty("offlineShare", "0.02"));
    private final int offlineEvery = Integer.getInteger("offlineEvery", 60);
    private final int killAt = Integer.getInteger("killAt", 120);
    private final int restartAt = Integer.getInteger("restartAt", 180);
    private final int webhookDownFrom = Integer.getInteger("webhookDownFrom", 60);
    private final int webhookDownTo = Integer.getInteger("webhookDownTo", 90);
    private final int maxInFlight = Integer.getInteger("maxInFlight", 2_000);
    private final String workers = System.getProperty("workers", "16");
    private final String pool = System.getProperty("pool", "40");

    // Generator state
    private final List<TraccarInstance> instances = new CopyOnWriteArrayList<>();
    private final AtomicReferenceArray<TraccarInstance> slots;
    private final HttpClient client = HttpClient.newBuilder().executor(Executors.newFixedThreadPool(32)).build();
    private final Semaphore inFlight;
    private final ConcurrentLinkedQueue<String[]> retries = new ConcurrentLinkedQueue<>(); // {device, item}
    private record Pending(int device, List<String> items) { }
    private final ConcurrentLinkedQueue<Pending> outgoing = new ConcurrentLinkedQueue<>();
    private final ConcurrentLinkedQueue<Long> latencies = new ConcurrentLinkedQueue<>();
    private final AtomicLong offered = new AtomicLong();
    private final AtomicLong acknowledged = new AtomicLong();
    private final AtomicLong refusedBusy = new AtomicLong();
    private final AtomicLong lockFailures = new AtomicLong();
    private final AtomicLong otherFailures = new AtomicLong();
    private final AtomicLong transportErrors = new AtomicLong();
    private final AtomicLong retried = new AtomicLong();
    private final AtomicLong waitingToSend = new AtomicLong();
    private final AtomicLong webhookCalls = new AtomicLong();
    private final AtomicLong distinctReports = new AtomicLong();
    private volatile boolean webhookDown;
    private final Instant base = Instant.now().minusSeconds(3600);

    private LoadTest() {
        slots = new AtomicReferenceArray<>(instancesCount);
        inFlight = new Semaphore(maxInFlight);
    }

    private static void log(String format, Object... args) {
        System.out.printf("[%s] %s%n", Instant.now().toString().substring(11, 19), String.format(format, args));
    }

    public static void main(String[] args) {
        int status = 0;
        try {
            new LoadTest().run();
        } catch (Throwable e) {
            e.printStackTrace();
            status = 1;
        }
        System.exit(status); // in-process servers leave non-daemon threads behind
    }

    private void run() throws Exception {
        Path pgBin = Path.of(System.getProperty("pgBin"));
        Path work = Files.createTempDirectory("load-test");
        int port = freePort();
        Path data = work.resolve("data");
        exec(pgBin.resolve("initdb").toString(), "-D", data.toString(), "-U", "postgres", "-A", "trust",
                "-E", "UTF8", "--locale=C");
        // Durability settings as in production (fsync, synchronous_commit on)
        exec(pgBin.resolve("pg_ctl").toString(), "-D", data.toString(), "-l", work.resolve("pg.log").toString(),
                "-o", "-p " + port + " -h 127.0.0.1 -c unix_socket_directories='' -c max_connections=500"
                        + " -c shared_buffers=1GB -c max_wal_size=8GB -c shared_preload_libraries=pg_stat_statements"
                        + " -c timezone=America/Sao_Paulo", "-w", "start");
        String url = "jdbc:postgresql://127.0.0.1:" + port + "/traccar_load";
        try (Connection admin = DriverManager.getConnection(
                "jdbc:postgresql://127.0.0.1:" + port + "/postgres", "postgres", "")) {
            admin.createStatement().execute("CREATE DATABASE traccar_load");
        }
        HttpServer webhook = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        webhook.setExecutor(Executors.newFixedThreadPool(16));
        webhook.createContext("/events", exchange -> {
            exchange.getRequestBody().readAllBytes();
            webhookCalls.incrementAndGet();
            exchange.sendResponseHeaders(webhookDown ? 500 : 200, -1);
            exchange.close();
        });
        webhook.start();
        String webhookUrl = "http://127.0.0.1:" + webhook.getAddress().getPort() + "/events";
        try {
            log("Starting %d servers on PostgreSQL %s", instancesCount, scalar(url, "SHOW server_version"));
            for (int i = 0; i < instancesCount; i++) {
                slots.set(i, startInstance(work, url, webhookUrl, i));
            }
            seed(url);
            sql(url, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements");
            sql(url, "SELECT pg_stat_statements_reset()");
            generate(work, url, webhookUrl, port);
        } finally {
            for (var instance : instances) {
                try {
                    instance.close();
                } catch (Exception ignored) {
                    // already stopped
                }
            }
            webhook.stop(0);
            exec(pgBin.resolve("pg_ctl").toString(), "-D", data.toString(), "-m", "fast", "-w", "stop");
            log("Cluster stopped; data left in %s (delete it when done)", work);
        }
    }

    private TraccarInstance startInstance(Path work, String url, String webhookUrl, int index) throws Exception {
        Path directory = Files.createDirectories(work.resolve("server" + index + "-" + System.nanoTime()));
        var instance = new TraccarInstance(directory, url, webhookUrl, Map.of(
                "database.password", "",
                "database.maxPoolSize", pool,
                "processing.workers", workers,
                "ingest.batchesPerMinute", "0",
                "ingest.maxAge", String.valueOf(14 * 86400),
                "report.trip.minDistance", "200",
                "report.trip.minDuration", "60"));
        instance.storage(); // the schema is created (Liquibase) when storage is first used
        instances.add(instance);
        return instance;
    }

    private double centerLat(int geofence) {
        return -23.5 + (geofence / 50) * 0.02;
    }

    private double centerLon(int geofence) {
        return -46.8 + (geofence % 50) * 0.02;
    }

    /** Devices, geofences (300 m circles on a grid) and links: each device to its own fence and others. */
    private void seed(String url) throws SQLException {
        log("Seeding %,d devices, %,d geofences, %d links per device", devices, geofences, linksPerDevice);
        sql(url, "INSERT INTO tc_devices (name, uniqueid, attributes) SELECT 'device' || g, 'load-' || g, '{}'"
                + " FROM generate_series(0, " + (devices - 1) + ") g");
        sql(url, "INSERT INTO tc_geofences (name, area, attributes) SELECT 'fence' || g, 'CIRCLE ('"
                + " || (-23.5 + (g / 50) * 0.02) || ' ' || (-46.8 + (g % 50) * 0.02) || ', 300)', '{}'"
                + " FROM generate_series(0, " + (geofences - 1) + ") g");
        sql(url, "INSERT INTO tc_device_geofence (deviceid, geofenceid)"
                + " SELECT d.id, f.id FROM tc_devices d CROSS JOIN generate_series(0, " + (linksPerDevice - 1) + ") k"
                + " JOIN tc_geofences f ON f.name = 'fence' || ((substring(d.uniqueid FROM 6)::int + k * 97) % "
                + geofences + ")");
    }

    /** Position of a device at a tick: alternately inside and 1.1 km outside its own geofence. */
    private String item(int device, long tick, String suffix, double offsetSeconds) {
        int fence = device % geofences;
        long ticksPerHalf = Math.max(1, crossingSeconds / 2 / interval);
        boolean inside = (tick / ticksPerHalf) % 2 == 0;
        double jitter = ((device * 31 + tick * 17) % 100) * 0.00001;
        double lat = centerLat(fence) + (inside ? jitter : 0.01 + jitter);
        Instant time = base.plusSeconds(tick * interval).plusMillis((long) (offsetSeconds * 1000)
                + (device % interval) * 1000L);
        return "{\"id\":\"d" + device + "-t" + tick + suffix + "\",\"timestamp\":\"" + time + "\",\"lat\":" + lat
                + ",\"lon\":" + centerLon(fence) + ",\"speed\":" + (inside ? 0 : 25) + ",\"batt\":80}";
    }

    private TraccarInstance route(int device) {
        // Device affinity, like a consumer partitioning by device; a dead server's devices move to the others
        for (int i = 0; i < instancesCount; i++) {
            var instance = slots.get((device + i) % instancesCount);
            if (instance != null) {
                return instance;
            }
        }
        throw new IllegalStateException("no server alive");
    }

    /** Queues reports like a message queue would hold them; the sender drains it within maxInFlight. */
    private void send(int device, List<String> items) {
        offered.addAndGet(items.size());
        waitingToSend.addAndGet(items.size());
        outgoing.add(new Pending(device, items));
    }

    private void sender() {
        while (!Thread.currentThread().isInterrupted()) {
            Pending pending = outgoing.poll();
            if (pending == null) {
                try {
                    Thread.sleep(1);
                } catch (InterruptedException e) {
                    return;
                }
                continue;
            }
            try {
                inFlight.acquire();
            } catch (InterruptedException e) {
                return;
            }
            waitingToSend.addAndGet(-pending.items().size());
            dispatch(pending.device(), pending.items());
        }
    }

    private void dispatch(int device, List<String> items) {
        TraccarInstance target;
        try {
            target = route(device);
        } catch (IllegalStateException e) {
            inFlight.release();
            items.forEach(item -> retries.add(new String[] {String.valueOf(device), item}));
            return;
        }
        String body = "{\"deviceId\":\"load-" + device + "\",\"positions\":[" + String.join(",", items) + "]}";
        long start = System.nanoTime();
        var request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + target.port() + "/api/positions/batch"))
                .timeout(Duration.ofSeconds(60))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body)).build();
        client.sendAsync(request, HttpResponse.BodyHandlers.ofString()).whenComplete((response, error) -> {
            inFlight.release();
            latencies.add((System.nanoTime() - start) / 1_000_000);
            if (error != null || response.statusCode() >= 400 && response.statusCode() != 503) {
                transportErrors.incrementAndGet();
                items.forEach(item -> retries.add(new String[] {String.valueOf(device), item}));
                return;
            }
            var matcher = STATUS.matcher(response.body());
            int index = 0;
            while (matcher.find() && index < items.size()) {
                String status = matcher.group(1);
                if (status.equals("failed")) {
                    String reason = matcher.group(2);
                    if ("busy".equals(reason)) {
                        refusedBusy.incrementAndGet();
                    } else if ("lock".equals(reason)) {
                        lockFailures.incrementAndGet();
                    } else {
                        otherFailures.incrementAndGet();
                    }
                    retries.add(new String[] {String.valueOf(device), items.get(index)});
                } else {
                    acknowledged.incrementAndGet();
                }
                index++;
            }
        });
    }

    private void generate(Path work, String url, String webhookUrl, int pgPort) throws Exception {
        var random = new Random(1);
        boolean[] offline = new boolean[devices];
        for (int d = 0; d < devices; d++) {
            offline[d] = random.nextDouble() < offlineShare;
        }
        Thread senderThread = new Thread(this::sender, "sender");
        senderThread.setDaemon(true);
        senderThread.start();
        ScheduledExecutorService sampler = Executors.newSingleThreadScheduledExecutor();
        var metrics = new Metrics(url, pgPort);
        sampler.scheduleAtFixedRate(metrics::sample, 5, 5, TimeUnit.SECONDS);
        log("Load: %,d devices every %d s = %,d positions/s offered; %.0f%% also re-send shuffled offline batches"
                        + " every %d s; server 2 killed at %d s and restarted at %d s; webhook down %d-%d s",
                devices, interval, devices / interval, offlineShare * 100, offlineEvery, killAt, restartAt,
                webhookDownFrom, webhookDownTo);
        long started = System.nanoTime();
        long ticks = (long) duration / interval;
        int perSecond = devices / interval;
        for (long second = 0; second < duration; second++) {
            long elapsed = second;
            webhookDown = elapsed >= webhookDownFrom && elapsed < webhookDownTo;
            if (elapsed == killAt && instancesCount > 1) {
                var victim = slots.getAndSet(1, null);
                log(">>> crashing server 2: its database connections drop (open transactions roll back),"
                        + " then its network connections; unfinished items are retried elsewhere");
                if (victim.get(DataSource.class) instanceof HikariDataSource hikari) {
                    hikari.close();
                }
                victim.close();
                instances.remove(victim);
            }
            if (elapsed == restartAt && instancesCount > 1 && slots.get(1) == null) {
                log(">>> restarting server 2");
                slots.set(1, startInstance(work, url, webhookUrl, 1));
            }
            long tick = second / interval;
            int slice = (int) (second % interval);
            // Spread this second's devices over ten 100 ms steps
            for (int step = 0; step < 10; step++) {
                long stepStart = System.nanoTime();
                for (int k = step * perSecond / 10; k < (step + 1) * perSecond / 10; k++) {
                    int device = k * interval + slice;
                    if (device >= devices) {
                        continue;
                    }
                    send(device, List.of(item(device, tick, "", 0)));
                    distinctReports.incrementAndGet();
                    if (offline[device] && tick > 0 && (tick * interval) % offlineEvery == 0) {
                        // The phone's queue: the last reports again (duplicates), plus some never sent live (late)
                        var batch = new ArrayList<String>();
                        for (long back = 1; back <= offlineEvery / interval && back <= tick; back++) {
                            batch.add(item(device, tick - back, "", 0));
                            batch.add(item(device, tick - back, "b", interval / 2.0));
                            distinctReports.incrementAndGet();
                        }
                        Collections.shuffle(batch, random);
                        send(device, batch);
                    }
                }
                String[] retry;
                int retriesThisStep = 0;
                while (retriesThisStep < 2_000 && (retry = retries.poll()) != null) {
                    retried.incrementAndGet();
                    send(Integer.parseInt(retry[0]), List.of(retry[1]));
                    retriesThisStep++;
                }
                long sleep = 100_000_000 - (System.nanoTime() - stepStart);
                if (sleep > 0) {
                    TimeUnit.NANOSECONDS.sleep(sleep);
                }
            }
        }
        log("Traffic stopped after %d s; draining retries and the outbox", (System.nanoTime() - started) / 1_000_000_000);
        webhookDown = false;
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(180);
        while (System.nanoTime() < deadline && (!retries.isEmpty() || !outgoing.isEmpty()
                || inFlight.availablePermits() < maxInFlight || metrics.outboxPending() > 0)) {
            String[] retry;
            while ((retry = retries.poll()) != null) {
                retried.incrementAndGet();
                send(Integer.parseInt(retry[0]), List.of(retry[1]));
            }
            Thread.sleep(1000);
        }
        sampler.shutdown();
        sampler.awaitTermination(10, TimeUnit.SECONDS);
        metrics.sample();
        metrics.summary(ticks);
    }

    /** Samples every 5 s, from the database, the servers and the generator. */
    private final class Metrics {

        private final String url;
        private final int pgPort;
        private long lastSequence;
        private long lastCommits;
        private long lastAcknowledged;
        private long lastOffered;
        private long lastWebhook;
        private long lastCpuTicks;
        private long lastRead;
        private long lastWrite;
        private long lastNanos = System.nanoTime();
        private final long startNanos = System.nanoTime();
        private double peakDurable;
        private boolean headerPrinted;

        Metrics(String url, int pgPort) {
            this.url = url;
            this.pgPort = pgPort;
        }

        long outboxPending() {
            try {
                return Long.parseLong(scalar(url, "SELECT count(*) FROM tc_outbox WHERE deliveredat IS NULL"));
            } catch (SQLException e) {
                return -1;
            }
        }

        synchronized void sample() {
            try {
                long now = System.nanoTime();
                double seconds = (now - lastNanos) / 1e9;
                lastNanos = now;
                long sequence = Long.parseLong(scalar(url, "SELECT last_value FROM tc_positions_id_seq"));
                long commits = Long.parseLong(scalar(url,
                        "SELECT xact_commit FROM pg_stat_database WHERE datname = 'traccar_load'"));
                String outbox = scalar(url, "SELECT count(*) || '/' || coalesce(extract(epoch FROM now()"
                        + " - min(createdat))::int, 0) FROM tc_outbox WHERE deliveredat IS NULL");
                String waits = scalar(url, "SELECT count(*) FROM pg_locks WHERE NOT granted");
                long[] cpuIo = postgresCpuAndIo();
                var sorted = new ArrayList<Long>();
                Long latency;
                while ((latency = latencies.poll()) != null) {
                    sorted.add(latency);
                }
                Collections.sort(sorted);
                int pending = 0;
                int active = 0;
                int awaiting = 0;
                for (var instance : instances) {
                    try {
                        pending += instance.get(ProcessingHandler.class).getPendingPositions();
                        DataSource dataSource = instance.get(DataSource.class);
                        if (dataSource instanceof HikariDataSource hikari && hikari.getHikariPoolMXBean() != null) {
                            active += hikari.getHikariPoolMXBean().getActiveConnections();
                            awaiting += hikari.getHikariPoolMXBean().getThreadsAwaitingConnection();
                        }
                    } catch (RuntimeException ignored) {
                        // a server going down
                    }
                }
                double durable = (sequence - lastSequence) / seconds;
                if (lastSequence > 0) {
                    peakDurable = Math.max(peakDurable, durable);
                }
                Runtime runtime = Runtime.getRuntime();
                if (!headerPrinted) {
                    System.out.println("  time | offered/s acked/s durable/s | ack ms p50/p95/p99 | in-flight"
                            + " to-send retry-q | workers-q pool act/wait | db tx/s cpu% rdMB/s wrMB/s lock-waits"
                            + " | outbox n/oldest-s hook/s | refused lock other transport | heap MB");
                    headerPrinted = true;
                }
                System.out.printf("%6d | %9.0f %7.0f %9.0f | %5d/%5d/%5d | %9d %7d %7d | %9d %4d/%4d"
                                + " | %6.0f %4.0f %6.1f %6.1f %10s | %10s %6.0f | %d %d %d %d | %d%n",
                        (now - startNanos) / 1_000_000_000,
                        (offered.get() - lastOffered) / seconds, (acknowledged.get() - lastAcknowledged) / seconds,
                        lastSequence > 0 ? durable : 0,
                        percentile(sorted, 50), percentile(sorted, 95), percentile(sorted, 99),
                        maxInFlight - inFlight.availablePermits(), waitingToSend.get(), retries.size(),
                        pending, active, awaiting,
                        (commits - lastCommits) / seconds, (cpuIo[0] - lastCpuTicks) / seconds,
                        (cpuIo[1] - lastRead) / seconds / 1e6, (cpuIo[2] - lastWrite) / seconds / 1e6, waits,
                        outbox, (webhookCalls.get() - lastWebhook) / seconds,
                        refusedBusy.get(), lockFailures.get(), otherFailures.get(), transportErrors.get(),
                        (runtime.totalMemory() - runtime.freeMemory()) / 1_000_000);
                lastSequence = sequence;
                lastCommits = commits;
                lastOffered = offered.get();
                lastAcknowledged = acknowledged.get();
                lastWebhook = webhookCalls.get();
                lastCpuTicks = cpuIo[0];
                lastRead = cpuIo[1];
                lastWrite = cpuIo[2];
            } catch (Exception e) {
                log("sample failed: %s", e);
            }
        }

        /** PostgreSQL processes: CPU in clock ticks (100 = one core-second), bytes read and written. */
        private long[] postgresCpuAndIo() throws IOException {
            long cpu = 0;
            long read = 0;
            long write = 0;
            var current = new java.util.HashMap<Long, Long>();
            String port = String.valueOf(pgPort);
            try (var proc = Files.list(Path.of("/proc"))) {
                for (Path dir : proc.filter(p -> p.getFileName().toString().matches("\\d+")).toList()) {
                    try {
                        String cmdline = Files.readString(dir.resolve("cmdline")).replace('\0', ' ');
                        String stat = Files.readString(dir.resolve("stat"));
                        boolean ours = cmdline.contains("-p " + port) || cmdline.startsWith("postgres:")
                                && isChildOfPostmaster(stat, port);
                        if (!ours) {
                            continue;
                        }
                        String[] fields = stat.substring(stat.lastIndexOf(')') + 2).split(" ");
                        long ticks = Long.parseLong(fields[11]) + Long.parseLong(fields[12]);
                        long pid = Long.parseLong(dir.getFileName().toString());
                        cpu += ticks - previousTicks.getOrDefault(pid, ticks == 0 ? 0L : firstSample ? ticks : 0L);
                        current.put(pid, ticks);
                        for (String line : Files.readAllLines(dir.resolve("io"))) {
                            if (line.startsWith("read_bytes:")) {
                                read += Long.parseLong(line.substring(12).trim());
                            } else if (line.startsWith("write_bytes:")) {
                                write += Long.parseLong(line.substring(13).trim());
                            }
                        }
                    } catch (IOException | RuntimeException ignored) {
                        // process gone or not ours
                    }
                }
            }
            previousTicks = current;
            firstSample = false;
            cpuTotal += cpu;
            return new long[] {cpuTotal, read, write};
        }

        // CPU as the sum of per-process increments, so exiting backends don't make it go backwards
        private Map<Long, Long> previousTicks = new java.util.HashMap<>();
        private boolean firstSample = true;
        private long cpuTotal;

        private Long postmaster;

        private boolean isChildOfPostmaster(String stat, String port) throws IOException {
            if (postmaster == null) {
                try (var proc = Files.list(Path.of("/proc"))) {
                    for (Path dir : proc.filter(p -> p.getFileName().toString().matches("\\d+")).toList()) {
                        try {
                            if (Files.readString(dir.resolve("cmdline")).replace('\0', ' ').contains("-p " + port)) {
                                postmaster = Long.parseLong(dir.getFileName().toString());
                            }
                        } catch (IOException ignored) {
                            // gone
                        }
                    }
                }
            }
            String[] fields = stat.substring(stat.lastIndexOf(')') + 2).split(" ");
            return postmaster != null && Long.parseLong(fields[1]) == postmaster;
        }

        private long percentile(List<Long> sorted, int percent) {
            return sorted.isEmpty() ? 0 : sorted.get(Math.min(sorted.size() - 1, sorted.size() * percent / 100));
        }

        void summary(long ticks) throws SQLException {
            log("Summary");
            long positions = Long.parseLong(scalar(url, "SELECT count(*) FROM tc_positions"));
            long receipts = Long.parseLong(scalar(url, "SELECT count(*) FROM tc_position_receipts"));
            log("  distinct reports generated: %,d; stored positions: %,d; receipts: %,d (equal = each report once)",
                    distinctReports.get(), positions, receipts);
            log("  offered %,d items (with duplicates and retries), retried %,d; refused busy %,d, lock %,d,"
                    + " other failed %,d, transport errors %,d", offered.get(), retried.get(), refusedBusy.get(),
                    lockFailures.get(), otherFailures.get(), transportErrors.get());
            log("  peak durable rate %.0f positions/s against %,d/s offered", peakDurable, devices / interval);
            log("  events %s, outbox undelivered %s, webhook calls %,d (failed attempts included)",
                    scalar(url, "SELECT count(*) FROM tc_events"), outboxPending(), webhookCalls.get());
            log("  late intervals recorded: %s", scalar(url, "SELECT count(*) FROM tc_late_positions"));
            log("  top statements by total time (pg_stat_statements):");
            try (Connection connection = DriverManager.getConnection(url, "postgres", "");
                 var rs = connection.createStatement().executeQuery(
                         "SELECT calls, round(mean_exec_time::numeric, 2), round(max_exec_time::numeric, 1),"
                                 + " left(regexp_replace(query, '\\s+', ' ', 'g'), 90) FROM pg_stat_statements"
                                 + " ORDER BY total_exec_time DESC LIMIT 12")) {
                while (rs.next()) {
                    System.out.printf("    %10d calls  mean %7s ms  max %8s ms  %s%n",
                            rs.getLong(1), rs.getString(2), rs.getString(3), rs.getString(4));
                }
            }
        }
    }

    private static int freePort() throws IOException {
        try (var socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static void sql(String url, String statement) throws SQLException {
        try (Connection connection = DriverManager.getConnection(url, "postgres", "")) {
            connection.createStatement().execute(statement);
        }
    }

    private static String scalar(String url, String query) throws SQLException {
        try (Connection connection = DriverManager.getConnection(url, "postgres", "");
             var rs = connection.createStatement().executeQuery(query)) {
            rs.next();
            return rs.getString(1);
        }
    }

    private static void exec(String... command) throws IOException, InterruptedException {
        var process = new ProcessBuilder(command).redirectErrorStream(true).start();
        String output = new String(process.getInputStream().readAllBytes());
        if (process.waitFor() != 0) {
            throw new IOException(Arrays.toString(command) + " failed:\n" + output);
        }
    }

}
