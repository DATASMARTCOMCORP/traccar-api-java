package org.traccar;

import io.netty.channel.Channel;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;

import java.util.HashSet;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Threads that process positions, apart from the network threads. A device always goes to the same worker, so
 * a device waiting for its lock (another server is processing it) holds up only the devices sharing that
 * worker, never the network. Each worker bounds the positions waiting for it: past {@code limit} the
 * connections sending to it stop being read until it catches up; past twice that, positions are refused.
 */
final class ProcessingWorkers {

    private static final Logger LOGGER = LoggerFactory.getLogger(ProcessingWorkers.class);

    final class Worker {

        private final ExecutorService executor;
        private final AtomicInteger pending = new AtomicInteger();
        private final Set<Channel> paused = new HashSet<>();
        private volatile Thread thread;

        private Worker(int index) {
            executor = Executors.newSingleThreadExecutor(runnable -> {
                Thread created = new Thread(runnable, "processing-" + index);
                created.setDaemon(true);
                thread = created;
                return created;
            });
        }

        /** Counts a position in; false when the worker is full and the position must be refused. */
        boolean admit(Channel channel) {
            int count = pending.incrementAndGet();
            if (count > 2 * limit) {
                pending.decrementAndGet();
                LOGGER.warn("Processing worker full, position refused");
                return false;
            }
            if (count > limit && channel != null) {
                synchronized (paused) {
                    if (paused.add(channel)) {
                        channel.config().setAutoRead(false);
                    }
                }
            }
            return true;
        }

        /** Counts a finished position out, resuming the paused connections once the backlog is down. */
        void release() {
            if (pending.decrementAndGet() <= limit / 2) {
                synchronized (paused) {
                    for (Channel channel : paused) {
                        channel.config().setAutoRead(true);
                    }
                    paused.clear();
                }
            }
        }

        int pending() {
            return pending.get();
        }

        void execute(Runnable task) {
            executor.execute(task);
        }

        /** Runs the task now when already on this worker, so a synchronous chain stays on one stack. */
        void run(Runnable task) {
            if (Thread.currentThread() == thread) {
                task.run();
            } else {
                executor.execute(task);
            }
        }

        void shutdown() {
            executor.shutdownNow();
        }
    }

    private final Worker[] workers;
    private final int limit;

    ProcessingWorkers(Config config) {
        limit = config.getInteger(Keys.PROCESSING_QUEUE_LIMIT);
        workers = new Worker[config.getInteger(Keys.PROCESSING_WORKERS)];
        for (int i = 0; i < workers.length; i++) {
            workers[i] = new Worker(i);
        }
    }

    Worker of(long deviceId) {
        return workers[Math.floorMod(Long.hashCode(deviceId), workers.length)];
    }

    void shutdown() {
        for (Worker worker : workers) {
            worker.shutdown();
        }
    }

}
