package org.traccar.database;

import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;

import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.Statement;
import java.sql.Timestamp;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;

/**
 * Deliveries that leave the server (event webhook, notifications, position forwarding), written in the same
 * transaction as the data they announce. A delivery is claimed for a while before it is attempted, and marked
 * delivered after it succeeds; one whose claim expires is attempted again, by this server or another. Delivery
 * is therefore at least once: a crash after sending and before marking resends it, and receivers must
 * de-duplicate by the id inside the payload (the event id or position id).
 */
@Singleton
public class OutboxStore {

    public static final String EVENT = "event";
    public static final String NOTIFY = "notify";
    public static final String POSITION = "position";

    public record Entry(long id, String kind, long deviceId, long objectId, int attempts) { }

    private static final Logger LOGGER = LoggerFactory.getLogger(OutboxStore.class);

    private final Storage storage;
    private final long lease;

    @Inject
    public OutboxStore(Config config, Storage storage) {
        this.storage = storage;
        lease = config.getLong(Keys.OUTBOX_LEASE);
        long timeouts = config.getInteger(Keys.CLIENT_CONNECT_TIMEOUT) + config.getInteger(Keys.CLIENT_READ_TIMEOUT);
        if (lease <= timeouts) {
            LOGGER.warn("outbox.lease ({} ms) does not exceed the HTTP client timeouts ({} ms):"
                    + " slow deliveries may be sent twice", lease, timeouts);
        }
    }

    private static Timestamp now(long offsetMillis) {
        return new Timestamp(System.currentTimeMillis() + offsetMillis);
    }

    /** Adds a delivery in the current transaction. */
    public Entry add(String kind, long deviceId, long objectId) throws StorageException {
        return storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "INSERT INTO tc_outbox (kind, deviceid, objectid, createdat, attempts) VALUES (?, ?, ?, ?, 0)",
                    Statement.RETURN_GENERATED_KEYS)) {
                statement.setString(1, kind);
                statement.setLong(2, deviceId);
                statement.setLong(3, objectId);
                statement.setTimestamp(4, now(0));
                statement.executeUpdate();
                try (ResultSet keys = statement.getGeneratedKeys()) {
                    keys.next();
                    return new Entry(keys.getLong(1), kind, deviceId, objectId, 0);
                }
            }
        });
    }

    /** Takes a delivery for the lease time; false when another attempt holds it or it is done. */
    public boolean claim(Entry entry) throws StorageException {
        return storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "UPDATE tc_outbox SET claimeduntil = ?, attempts = attempts + 1"
                            + " WHERE id = ? AND deliveredat IS NULL AND (claimeduntil IS NULL OR claimeduntil < ?)")) {
                statement.setTimestamp(1, now(lease));
                statement.setLong(2, entry.id());
                statement.setTimestamp(3, now(0));
                return statement.executeUpdate() == 1;
            }
        });
    }

    /** Deliveries nobody holds, oldest first. */
    public List<Entry> pending(int limit) throws StorageException {
        return storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "SELECT id, kind, deviceid, objectid, attempts FROM tc_outbox"
                            + " WHERE deliveredat IS NULL AND (claimeduntil IS NULL OR claimeduntil < ?)"
                            + " ORDER BY id")) {
                statement.setTimestamp(1, now(0));
                statement.setMaxRows(limit);
                List<Entry> result = new ArrayList<>();
                try (ResultSet resultSet = statement.executeQuery()) {
                    while (resultSet.next()) {
                        result.add(new Entry(resultSet.getLong(1), resultSet.getString(2), resultSet.getLong(3),
                                resultSet.getLong(4), resultSet.getInt(5)));
                    }
                }
                return result;
            }
        });
    }

    public void delivered(Entry entry) throws StorageException {
        storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "UPDATE tc_outbox SET deliveredat = ? WHERE id = ?")) {
                statement.setTimestamp(1, now(0));
                statement.setLong(2, entry.id());
                return statement.executeUpdate();
            }
        });
    }

    /** Keeps a failed delivery claimed until it may be attempted again: 5 s doubling up to 10 min. */
    public void retryLater(Entry entry) throws StorageException {
        long delay = Math.min(600_000L, 5_000L << Math.min(entry.attempts(), 7));
        storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "UPDATE tc_outbox SET claimeduntil = ? WHERE id = ? AND deliveredat IS NULL")) {
                statement.setTimestamp(1, now(delay));
                statement.setLong(2, entry.id());
                return statement.executeUpdate();
            }
        });
    }

    public void removeDelivered(Date before) throws StorageException {
        storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "DELETE FROM tc_outbox WHERE deliveredat < ?")) {
                statement.setTimestamp(1, new Timestamp(before.getTime()));
                return statement.executeUpdate();
            }
        });
    }

}
