package org.traccar.storage;

/**
 * Result of storing a position under a report id: the stored row, or the row an earlier delivery of the same
 * report already stored.
 */
public record StoredPosition(long positionId, boolean duplicate) {
}
