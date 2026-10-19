package org.traccar.schedule;

import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;

import java.util.Date;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * Deletes position receipts a day after their reports fall out of the batch endpoint's accepted age. A report
 * older than that is rejected as too old, so it can't be stored a second time once its receipt is gone.
 */
public class TaskDeleteReceipts extends SingleScheduleTask {

    private static final Logger LOGGER = LoggerFactory.getLogger(TaskDeleteReceipts.class);

    private static final long CHECK_PERIOD_HOURS = 1;

    private final Storage storage;
    private final long retention;

    @Inject
    public TaskDeleteReceipts(Config config, Storage storage) {
        this.storage = storage;
        retention = TimeUnit.SECONDS.toMillis(config.getLong(Keys.INGEST_MAX_AGE)) + TimeUnit.DAYS.toMillis(1);
    }

    @Override
    public void schedule(ScheduledExecutorService executor) {
        executor.scheduleAtFixedRate(this, CHECK_PERIOD_HOURS, CHECK_PERIOD_HOURS, TimeUnit.HOURS);
    }

    @Override
    public void run() {
        try {
            storage.removeReceipts(new Date(System.currentTimeMillis() - retention));
        } catch (StorageException e) {
            LOGGER.warn("Failed to delete position receipts", e);
        }
    }

}
