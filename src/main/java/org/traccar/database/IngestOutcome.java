package org.traccar.database;

/**
 * What happened to one report. STORED, DUPLICATE, REJECTED and QUARANTINED are final: the sender may forget
 * the report. QUARANTINED means it is kept aside, unprocessed, for review (outside the accepted age).
 * FAILED is ambiguous (the report may or may not be stored) and must be retried; retrying is safe because
 * stored reports are recognized by their id.
 */
public record IngestOutcome(Status status, String reason, long positionId) {

    public enum Status {
        STORED, DUPLICATE, REJECTED, QUARANTINED, FAILED;

        public boolean isFinal() {
            return this != FAILED;
        }
    }

    public static IngestOutcome stored(long positionId) {
        return new IngestOutcome(Status.STORED, null, positionId);
    }

    public static IngestOutcome duplicate(long positionId) {
        return new IngestOutcome(Status.DUPLICATE, null, positionId);
    }

    public static IngestOutcome rejected(String reason) {
        return new IngestOutcome(Status.REJECTED, reason, 0);
    }

    public static IngestOutcome quarantined(String reason) {
        return new IngestOutcome(Status.QUARANTINED, reason, 0);
    }

    public static IngestOutcome failed(String reason) {
        return new IngestOutcome(Status.FAILED, reason, 0);
    }

}
