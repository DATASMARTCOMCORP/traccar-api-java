package org.traccar.schedule;

import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.database.NotificationManager;
import org.traccar.database.OutboxStore;
import org.traccar.handler.PositionForwardingHandler;
import org.traccar.storage.StorageException;

import java.util.Date;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * Delivers outbox entries that were not delivered right after their commit: the server stopped in between,
 * or the receiver failed and the entry waits for its retry. Every server runs it; claims keep two servers
 * from taking the same entry at the same time.
 */
public class TaskOutbox implements ScheduleTask {

    private static final Logger LOGGER = LoggerFactory.getLogger(TaskOutbox.class);

    private static final long PERIOD_SECONDS = 30;
    private static final int BATCH = 500;
    private static final long SLOT_WAIT_MILLIS = 1000;

    private final OutboxStore outbox;
    private final NotificationManager notificationManager;
    private final PositionForwardingHandler positionForwardingHandler;

    @Inject
    public TaskOutbox(
            OutboxStore outbox, NotificationManager notificationManager,
            PositionForwardingHandler positionForwardingHandler) {
        this.outbox = outbox;
        this.notificationManager = notificationManager;
        this.positionForwardingHandler = positionForwardingHandler;
    }

    @Override
    public boolean multipleInstances() {
        return true;
    }

    @Override
    public void schedule(ScheduledExecutorService executor) {
        executor.scheduleWithFixedDelay(this, PERIOD_SECONDS, PERIOD_SECONDS, TimeUnit.SECONDS);
    }

    @Override
    public void run() {
        try {
            for (var entry : outbox.pending(BATCH)) {
                try {
                    // Each entry is claimed only once a delivery slot is free, right before it is sent
                    boolean started = entry.kind().equals(OutboxStore.POSITION)
                            ? positionForwardingHandler.redeliver(entry, SLOT_WAIT_MILLIS)
                            : notificationManager.redeliver(entry, SLOT_WAIT_MILLIS);
                    if (!started && outbox.idle() == 0) {
                        break; // all slots busy: the rest waits for the next run
                    }
                } catch (StorageException | RuntimeException e) {
                    LOGGER.warn("Outbox delivery failed", e);
                }
            }
            outbox.removeDelivered(new Date(System.currentTimeMillis() - TimeUnit.DAYS.toMillis(1)));
        } catch (StorageException e) {
            LOGGER.warn("Outbox scan failed", e);
        }
    }

}
