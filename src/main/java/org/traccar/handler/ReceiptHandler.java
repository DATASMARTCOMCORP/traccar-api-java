package org.traccar.handler;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.database.IngestOutcome;
import org.traccar.model.Position;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;

import java.sql.PreparedStatement;
import java.sql.Timestamp;
import java.util.concurrent.TimeUnit;

/**
 * First handler for reports with an identity (OsmAnd single requests and batches):
 * <ul>
 *   <li>a report that was already stored stops here, so a redelivery has no side effects;</li>
 *   <li>a report outside the accepted age ({@code ingest.maxAge}, {@code ingest.maxFuture}) is quarantined:
 *   kept in tc_position_quarantine for review, never processed. Receipts live as long as that age, so a
 *   report can't be stored twice even after its receipt is deleted.</li>
 * </ul>
 * It runs with the device locked, so the receipt it reads can't appear concurrently; DatabaseHandler's receipt
 * insert still enforces uniqueness against writers that don't lock.
 */
public class ReceiptHandler extends BasePositionHandler {

    private static final Logger LOGGER = LoggerFactory.getLogger(ReceiptHandler.class);

    private final Storage storage;
    private final ObjectMapper objectMapper;
    private final long maxAge;
    private final long maxFuture;

    @Inject
    public ReceiptHandler(Config config, Storage storage, ObjectMapper objectMapper) {
        this.storage = storage;
        this.objectMapper = objectMapper;
        maxAge = TimeUnit.SECONDS.toMillis(config.getLong(Keys.INGEST_MAX_AGE));
        maxFuture = TimeUnit.SECONDS.toMillis(config.getLong(Keys.INGEST_MAX_FUTURE));
    }

    @Override
    public void onPosition(Position position, Callback callback) {
        var ticket = position.getIngestTicket();
        if (ticket == null) {
            callback.processed(false);
            return;
        }
        try {
            long positionId = storage.getReceipt(position.getDeviceId(), ticket.getEventId());
            if (positionId != 0) {
                ticket.decide(IngestOutcome.duplicate(positionId));
                callback.processed(true);
                return;
            }
            long now = System.currentTimeMillis();
            long time = position.getFixTime().getTime();
            String reason = time < now - maxAge ? "too old" : time > now + maxFuture ? "future" : null;
            if (reason != null) {
                quarantine(position, ticket.getEventId(), reason);
                ticket.decide(IngestOutcome.quarantined(reason));
                callback.processed(true);
                return;
            }
        } catch (StorageException e) {
            LOGGER.warn("Failed to check position receipt", e);
            ticket.decide(IngestOutcome.failed("storage"));
            var transaction = storage.current();
            if (transaction != null) {
                transaction.setRollbackOnly();
            }
            callback.processed(true);
            return;
        }
        callback.processed(false);
    }

    private void quarantine(Position position, String eventId, String reason) throws StorageException {
        String json;
        try {
            json = objectMapper.writeValueAsString(position);
        } catch (JsonProcessingException e) {
            throw new StorageException(e);
        }
        storage.execute(connection -> {
            // The device is locked, so checking first is enough to keep one row per report
            try (PreparedStatement statement = connection.prepareStatement(
                    "INSERT INTO tc_position_quarantine (deviceid, eventid, fixtime, reason, receivedat, position)"
                            + " SELECT ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM tc_position_quarantine"
                            + " WHERE deviceid = ? AND eventid = ?)")) {
                statement.setLong(1, position.getDeviceId());
                statement.setString(2, eventId);
                statement.setTimestamp(3, new Timestamp(position.getFixTime().getTime()));
                statement.setString(4, reason);
                statement.setTimestamp(5, new Timestamp(System.currentTimeMillis()));
                statement.setString(6, json);
                statement.setLong(7, position.getDeviceId());
                statement.setString(8, eventId);
                return statement.executeUpdate();
            }
        });
    }

}
