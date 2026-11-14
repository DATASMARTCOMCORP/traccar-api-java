package org.traccar.metrics;

import io.opentelemetry.api.common.AttributeKey;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.metrics.DoubleHistogram;
import io.opentelemetry.api.metrics.LongCounter;
import io.opentelemetry.api.metrics.Meter;
import io.opentelemetry.api.metrics.MeterProvider;
import io.opentelemetry.api.metrics.ObservableLongMeasurement;
import io.opentelemetry.exporter.prometheus.PrometheusHttpServer;
import io.opentelemetry.sdk.metrics.SdkMeterProvider;
import io.opentelemetry.sdk.resources.Resource;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.LifecycleObject;
import org.traccar.config.Config;
import org.traccar.config.Keys;

import java.lang.management.BufferPoolMXBean;
import java.lang.management.GarbageCollectorMXBean;
import java.lang.management.ManagementFactory;
import java.lang.management.MemoryPoolMXBean;
import java.lang.management.MemoryType;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.Consumer;
import java.util.function.LongSupplier;

/**
 * Operational metrics of this server, served in the Prometheus text format at {@code /metrics} when
 * {@code metrics.enable} is set. Disabled, every instrument is a no-op and nothing listens.
 *
 * <p>Attributes stay bounded: protocol names, outcomes and fixed kinds only, never a device or its identifier.
 */
@Singleton
public class Metrics implements LifecycleObject {

    private static final Logger LOGGER = LoggerFactory.getLogger(Metrics.class);

    public static final AttributeKey<String> PROTOCOL = AttributeKey.stringKey("protocol");
    public static final AttributeKey<String> OUTCOME = AttributeKey.stringKey("outcome");
    public static final AttributeKey<String> RESULT = AttributeKey.stringKey("result");
    public static final AttributeKey<String> TYPE = AttributeKey.stringKey("type");
    public static final AttributeKey<String> STATE = AttributeKey.stringKey("state");

    private static final AttributeKey<String> SERVICE_NAME = AttributeKey.stringKey("service.name");
    private static final AttributeKey<String> SERVICE_VERSION = AttributeKey.stringKey("service.version");
    private static final AttributeKey<String> ENVIRONMENT = AttributeKey.stringKey("deployment.environment");
    private static final AttributeKey<String> MEMORY_TYPE = AttributeKey.stringKey("jvm.memory.type");
    private static final AttributeKey<String> MEMORY_POOL = AttributeKey.stringKey("jvm.memory.pool.name");
    private static final AttributeKey<String> GC_NAME = AttributeKey.stringKey("jvm.gc.name");
    private static final AttributeKey<String> BUFFER_POOL = AttributeKey.stringKey("jvm.buffer.pool.name");
    private static final AttributeKey<Boolean> THREAD_DAEMON = AttributeKey.booleanKey("jvm.thread.daemon");

    /** Seconds, from 1 ms to 30 s: database commits and locks sit at the low end, webhooks at the high end. */
    private static final List<Double> DURATION_BUCKETS = List.of(
            0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0);

    private static final double NANOS_PER_SECOND = 1_000_000_000.0;

    private final SdkMeterProvider meterProvider;
    private final Meter meter;

    private final Map<String, Attributes> protocolAttributes = new ConcurrentHashMap<>();
    private final Map<String, Attributes> outcomeAttributes = new ConcurrentHashMap<>();
    private final Map<String, Attributes> resultAttributes = new ConcurrentHashMap<>();
    private final Map<String, Attributes> typeAttributes = new ConcurrentHashMap<>();
    private final Map<String, Attributes> typeResultAttributes = new ConcurrentHashMap<>();

    private final LongCounter messagesReceived;
    private final LongCounter positionsDecoded;
    private final LongCounter decodingErrors;
    private final LongCounter positionsProcessed;
    private final DoubleHistogram processingDuration;
    private final DoubleHistogram lockDuration;
    private final LongCounter transactions;
    private final DoubleHistogram commitDuration;
    private final LongCounter forwardRequests;
    private final DoubleHistogram forwardDuration;

    @Inject
    public Metrics(Config config) {
        SdkMeterProvider provider = null;
        if (config.getBoolean(Keys.METRICS_ENABLE)) {
            String address = config.getString(Keys.METRICS_ADDRESS);
            int port = config.getInteger(Keys.METRICS_PORT);
            try {
                var attributes = Attributes.builder().put(SERVICE_NAME, "traccar");
                String version = Metrics.class.getPackage().getImplementationVersion();
                if (version != null) {
                    attributes.put(SERVICE_VERSION, version);
                }
                String environment = config.getString(Keys.METRICS_ENVIRONMENT);
                if (environment != null && !environment.isEmpty()) {
                    attributes.put(ENVIRONMENT, environment);
                }
                var server = PrometheusHttpServer.builder()
                        .setHost(address)
                        .setPort(port)
                        .setOtelScopeEnabled(false)
                        .setAllowedResourceAttributesFilter(ENVIRONMENT.getKey()::equals)
                        .build();
                provider = SdkMeterProvider.builder()
                        .setResource(Resource.getDefault().merge(Resource.create(attributes.build())))
                        .registerMetricReader(server)
                        .build();
                LOGGER.info("Metrics served at http://{}:{}/metrics", address, port);
            } catch (RuntimeException | LinkageError e) {
                // Metrics must never keep the tracking server from starting
                LOGGER.warn("Metrics endpoint {}:{} failed to start; metrics disabled", address, port, e);
            }
        }
        meterProvider = provider;
        meter = (provider != null ? provider : MeterProvider.noop()).get("org.traccar");

        messagesReceived = meter.counterBuilder("traccar.messages.received")
                .setUnit("{message}")
                .setDescription("Messages received from devices, before decoding")
                .build();
        positionsDecoded = meter.counterBuilder("traccar.positions.decoded")
                .setUnit("{position}")
                .setDescription("Positions decoded from device messages")
                .build();
        decodingErrors = meter.counterBuilder("traccar.decoding.errors")
                .setUnit("{error}")
                .setDescription("Device messages whose decoding threw; the connection is closed")
                .build();
        positionsProcessed = meter.counterBuilder("traccar.positions.processed")
                .setUnit("{position}")
                .setDescription("Positions leaving processing, by outcome")
                .build();
        processingDuration = meter.histogramBuilder("traccar.processing.duration")
                .setUnit("s")
                .setDescription("Time from locking the device to the end of the position transaction")
                .setExplicitBucketBoundariesAdvice(DURATION_BUCKETS)
                .build();
        lockDuration = meter.histogramBuilder("traccar.db.lock.duration")
                .setUnit("s")
                .setDescription("Time to open the position's transaction and lock its device")
                .setExplicitBucketBoundariesAdvice(DURATION_BUCKETS)
                .build();
        transactions = meter.counterBuilder("traccar.db.transactions")
                .setUnit("{transaction}")
                .setDescription("Position transactions: committed, rolled_back or commit_failed")
                .build();
        commitDuration = meter.histogramBuilder("traccar.db.commit.duration")
                .setUnit("s")
                .setDescription("Duration of position transaction commits, failed ones included")
                .setExplicitBucketBoundariesAdvice(DURATION_BUCKETS)
                .build();
        forwardRequests = meter.counterBuilder("traccar.forward.requests")
                .setUnit("{request}")
                .setDescription("Event and position forwarding attempts, by result")
                .build();
        forwardDuration = meter.histogramBuilder("traccar.forward.duration")
                .setUnit("s")
                .setDescription("Duration of event and position forwarding attempts")
                .setExplicitBucketBoundariesAdvice(DURATION_BUCKETS)
                .build();

        if (provider != null) {
            registerJvmMetrics();
        }
    }

    public boolean isEnabled() {
        return meterProvider != null;
    }

    /** Registers a gauge read on every scrape; the supplier must be cheap and thread-safe. */
    public void gauge(String name, String unit, String description, LongSupplier value) {
        if (isEnabled()) {
            meter.gaugeBuilder(name).ofLongs().setUnit(unit).setDescription(description)
                    .buildWithCallback(measurement -> measurement.record(value.getAsLong()));
        }
    }

    /** Registers a gauge with several attributed values, read on every scrape. */
    public void gaugeWithAttributes(
            String name, String unit, String description, Consumer<ObservableLongMeasurement> callback) {
        if (isEnabled()) {
            meter.gaugeBuilder(name).ofLongs().setUnit(unit).setDescription(description)
                    .buildWithCallback(callback);
        }
    }

    public Attributes protocol(String protocol) {
        return protocolAttributes.computeIfAbsent(protocol, key -> Attributes.of(PROTOCOL, key));
    }

    public void messageReceived(String protocol) {
        messagesReceived.add(1, protocol(protocol));
    }

    public void positionsDecoded(String protocol, int count) {
        if (count > 0) {
            positionsDecoded.add(count, protocol(protocol));
        }
    }

    public void decodingError(String protocol) {
        decodingErrors.add(1, protocol(protocol));
    }

    private Attributes outcome(String outcome) {
        return outcomeAttributes.computeIfAbsent(outcome, key -> Attributes.of(OUTCOME, key));
    }

    /** A position that never reached its handlers: {@code refused} (worker full) or {@code lock_failed}. */
    public void positionSkipped(String outcome) {
        positionsProcessed.add(1, outcome(outcome));
    }

    /** A position that went through processing; {@code startNanos} is when its transaction started. */
    public void positionProcessed(String outcome, long startNanos) {
        positionsProcessed.add(1, outcome(outcome));
        processingDuration.record(seconds(startNanos));
    }

    public void lockAcquired(long startNanos) {
        lockDuration.record(seconds(startNanos));
    }

    /** @param commitNanos duration of the commit, or a negative value when none was attempted */
    public void transactionFinished(String result, long commitNanos) {
        transactions.add(1, resultAttributes.computeIfAbsent(result, key -> Attributes.of(RESULT, key)));
        if (commitNanos >= 0) {
            commitDuration.record(commitNanos / NANOS_PER_SECOND);
        }
    }

    /** @param type {@code event} or {@code position} */
    public void forwarded(String type, long startNanos, boolean success) {
        String result = success ? "success" : "failure";
        forwardRequests.add(1, typeResultAttributes.computeIfAbsent(
                type + '/' + result, key -> Attributes.of(TYPE, type, RESULT, result)));
        forwardDuration.record(seconds(startNanos), typeAttributes.computeIfAbsent(
                type, key -> Attributes.of(TYPE, key)));
    }

    private static double seconds(long startNanos) {
        return (System.nanoTime() - startNanos) / NANOS_PER_SECOND;
    }

    private void registerJvmMetrics() {
        List<MemoryPoolMXBean> memoryPools = ManagementFactory.getMemoryPoolMXBeans();
        meter.upDownCounterBuilder("jvm.memory.used").setUnit("By")
                .setDescription("Memory used, by pool").buildWithCallback(measurement -> {
                    for (MemoryPoolMXBean pool : memoryPools) {
                        measurement.record(pool.getUsage().getUsed(), memoryAttributes(pool));
                    }
                });
        meter.upDownCounterBuilder("jvm.memory.committed").setUnit("By")
                .setDescription("Memory committed, by pool").buildWithCallback(measurement -> {
                    for (MemoryPoolMXBean pool : memoryPools) {
                        measurement.record(pool.getUsage().getCommitted(), memoryAttributes(pool));
                    }
                });
        meter.upDownCounterBuilder("jvm.memory.limit").setUnit("By")
                .setDescription("Maximum memory, by pool that has one").buildWithCallback(measurement -> {
                    for (MemoryPoolMXBean pool : memoryPools) {
                        long max = pool.getUsage().getMax();
                        if (max >= 0) {
                            measurement.record(max, memoryAttributes(pool));
                        }
                    }
                });

        List<BufferPoolMXBean> bufferPools = ManagementFactory.getPlatformMXBeans(BufferPoolMXBean.class);
        meter.upDownCounterBuilder("jvm.buffer.memory.used").setUnit("By")
                .setDescription("Direct and mapped buffer memory used").buildWithCallback(measurement -> {
                    for (BufferPoolMXBean pool : bufferPools) {
                        measurement.record(pool.getMemoryUsed(), Attributes.of(BUFFER_POOL, pool.getName()));
                    }
                });

        List<GarbageCollectorMXBean> collectors = ManagementFactory.getGarbageCollectorMXBeans();
        meter.counterBuilder("jvm.gc.collections").setUnit("{collection}")
                .setDescription("Garbage collections, by collector").buildWithCallback(measurement -> {
                    for (GarbageCollectorMXBean collector : collectors) {
                        long count = collector.getCollectionCount();
                        if (count >= 0) {
                            measurement.record(count, Attributes.of(GC_NAME, collector.getName()));
                        }
                    }
                });
        meter.counterBuilder("jvm.gc.time").ofDoubles().setUnit("s")
                .setDescription("Approximate time spent in garbage collection, by collector")
                .buildWithCallback(measurement -> {
                    for (GarbageCollectorMXBean collector : collectors) {
                        long millis = collector.getCollectionTime();
                        if (millis >= 0) {
                            measurement.record(millis / 1000.0, Attributes.of(GC_NAME, collector.getName()));
                        }
                    }
                });

        var threads = ManagementFactory.getThreadMXBean();
        meter.upDownCounterBuilder("jvm.thread.count").setUnit("{thread}")
                .setDescription("Live threads").buildWithCallback(measurement -> {
                    int daemon = threads.getDaemonThreadCount();
                    measurement.record(daemon, Attributes.of(THREAD_DAEMON, true));
                    measurement.record(threads.getThreadCount() - daemon, Attributes.of(THREAD_DAEMON, false));
                });

        var classes = ManagementFactory.getClassLoadingMXBean();
        meter.upDownCounterBuilder("jvm.class.count").setUnit("{class}")
                .setDescription("Classes currently loaded")
                .buildWithCallback(measurement -> measurement.record(classes.getLoadedClassCount()));

        var runtime = ManagementFactory.getRuntimeMXBean();
        meter.gaugeBuilder("process.uptime").setUnit("s")
                .setDescription("Time since the JVM started")
                .buildWithCallback(measurement -> measurement.record(runtime.getUptime() / 1000.0));

        if (ManagementFactory.getOperatingSystemMXBean() instanceof com.sun.management.OperatingSystemMXBean os) {
            meter.counterBuilder("jvm.cpu.time").ofDoubles().setUnit("s")
                    .setDescription("CPU time used by the process")
                    .buildWithCallback(measurement -> {
                        long nanos = os.getProcessCpuTime();
                        if (nanos >= 0) {
                            measurement.record(nanos / NANOS_PER_SECOND);
                        }
                    });
            meter.gaugeBuilder("jvm.cpu.recent_utilization").setUnit("1")
                    .setDescription("Recent CPU use of the process, from 0 to 1 over all processors")
                    .buildWithCallback(measurement -> {
                        double load = os.getProcessCpuLoad();
                        if (load >= 0) {
                            measurement.record(load);
                        }
                    });
        }
        if (ManagementFactory.getOperatingSystemMXBean() instanceof com.sun.management.UnixOperatingSystemMXBean os) {
            meter.upDownCounterBuilder("process.open_file_descriptors").setUnit("{file_descriptor}")
                    .setDescription("Open file descriptors, device connections included")
                    .buildWithCallback(measurement -> measurement.record(os.getOpenFileDescriptorCount()));
            meter.upDownCounterBuilder("process.max_file_descriptors").setUnit("{file_descriptor}")
                    .setDescription("Limit of open file descriptors")
                    .buildWithCallback(measurement -> measurement.record(os.getMaxFileDescriptorCount()));
        }
    }

    private static Attributes memoryAttributes(MemoryPoolMXBean pool) {
        return Attributes.of(
                MEMORY_TYPE, pool.getType() == MemoryType.HEAP ? "heap" : "non_heap",
                MEMORY_POOL, pool.getName());
    }

    @Override
    public void start() {
    }

    @Override
    public void stop() {
        if (meterProvider != null) {
            meterProvider.close();
        }
    }

}
