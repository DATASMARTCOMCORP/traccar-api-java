package org.traccar.database;

import java.util.concurrent.CompletableFuture;

/**
 * Travels with a position through the pipeline. The event id is the report's stable identity. Handlers
 * {@link #decide} an outcome as they go (the first decision wins); the processing chain completes the outcome
 * only after the position's transaction finished, so nothing is reported stored before it is committed.
 */
public final class IngestTicket {

    private final String eventId;
    private final CompletableFuture<IngestOutcome> outcome = new CompletableFuture<>();
    private volatile IngestOutcome decided;

    public IngestTicket(String eventId) {
        this.eventId = eventId;
    }

    public String getEventId() {
        return eventId;
    }

    public CompletableFuture<IngestOutcome> getOutcome() {
        return outcome;
    }

    public synchronized void decide(IngestOutcome value) {
        if (decided == null) {
            decided = value;
        }
    }

    public IngestOutcome getDecided() {
        return decided;
    }

    public void complete(IngestOutcome value) {
        outcome.complete(value);
    }

    /**
     * Identity derived from the fix itself, for reports that carry no id. Two reports of one device with the
     * same fix time and coordinates are the same report.
     */
    public static String fixId(long time, double latitude, double longitude) {
        return "fix:" + time + ":" + latitude + ":" + longitude;
    }

}
