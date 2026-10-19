package org.traccar.handler;

import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.database.IngestOutcome;
import org.traccar.model.Position;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;

/**
 * Stops a report that was already stored before it reaches any other handler, so a redelivery has no side
 * effects. This is a fast path only: DatabaseHandler's receipt insert is what guarantees a single row when
 * two deliveries race.
 */
public class ReceiptHandler extends BasePositionHandler {

    private static final Logger LOGGER = LoggerFactory.getLogger(ReceiptHandler.class);

    private final Storage storage;

    @Inject
    public ReceiptHandler(Storage storage) {
        this.storage = storage;
    }

    @Override
    public void onPosition(Position position, Callback callback) {
        var ticket = position.getIngestTicket();
        if (ticket != null) {
            try {
                long positionId = storage.getReceipt(position.getDeviceId(), ticket.getEventId());
                if (positionId != 0) {
                    ticket.complete(IngestOutcome.duplicate(positionId));
                    callback.processed(true);
                    return;
                }
            } catch (StorageException e) {
                LOGGER.warn("Failed to read position receipt", e);
            }
        }
        callback.processed(false);
    }

}
