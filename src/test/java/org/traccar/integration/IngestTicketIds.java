package org.traccar.integration;

import org.traccar.database.IngestTicket;

import java.time.Instant;

final class IngestTicketIds {

    private IngestTicketIds() {
    }

    static String fix(Instant time, double latitude, double longitude) {
        return IngestTicket.fixId(time.toEpochMilli(), latitude, longitude);
    }

}
