package org.traccar.protocol;

import org.junit.jupiter.api.Test;
import org.traccar.database.IngestOutcome;

import java.time.Instant;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;

class OsmAndPositionBatchTest {

    private static OsmAndPositionBatch.Batch parse(String body) {
        return OsmAndPositionBatch.parse(body);
    }

    @Test
    void sortsDeduplicatesAndSettlesInvalidFixes() {
        var batch = parse("""
                {"deviceId":" phone ","installId":"install","os":"android","positions":[
                  {"timestamp":1700000010,"lat":-22,"lon":-43,"speed":12,"batt":80,"is_moving":true},
                  {"timestamp":1700000000000,"lat":-23,"lon":-44},
                  {"timestamp":"2023-11-14T22:13:20Z","lat":-23,"lon":-44},
                  {"timestamp":1700000000,"lat":91,"lon":0},
                  {"timestamp":"invalid","lat":0,"lon":0}
                ]}
                """);
        assertEquals("phone", batch.deviceId());
        assertEquals(5, batch.items().size());
        assertEquals(2, batch.positions().size());
        assertEquals(Instant.ofEpochSecond(1700000000), batch.positions().getFirst().getFixTime().toInstant());
        var latest = batch.positions().getLast();
        assertEquals(12, latest.getSpeed());
        assertEquals(80.0, latest.getAttributes().get("batteryLevel"));
        assertEquals(true, latest.getAttributes().get("is_moving"));
        assertEquals("install", latest.getAttributes().get("installId"));

        var items = batch.items();
        assertSame(items.get(1), items.get(2).original());
        assertEquals(items.get(1).id(), items.get(2).id());
        assertEquals(IngestOutcome.rejected("invalid"), items.get(3).rejection());
        assertEquals(IngestOutcome.rejected("invalid"), items.get(4).rejection());
    }

    @Test
    void usesReportIdsOrTheFixAsIdentity() {
        var items = parse("""
                {"deviceId":"phone","positions":[
                  {"id":"a-1","timestamp":1700000000,"lat":-23,"lon":-44},
                  {"id":42,"timestamp":1700000000,"lat":-23,"lon":-44},
                  {"timestamp":1700000000,"lat":-23.5,"lon":-44.25},
                  {"id":"","timestamp":1700000000,"lat":-23,"lon":-44},
                  {"id":1.5,"timestamp":1700000000,"lat":-23,"lon":-44},
                  {"id":"%s","timestamp":1700000000,"lat":-23,"lon":-44}
                ]}
                """.formatted("x".repeat(129))).items();
        assertEquals("a-1", items.get(0).id());
        assertEquals("a-1", items.get(0).position().getIngestTicket().getEventId());
        assertEquals("42", items.get(1).id());
        assertNull(items.get(1).original(), "distinct ids are distinct reports even with the same fix");
        assertEquals("fix:1700000000000:-23.5:-44.25", items.get(2).id());
        for (int i = 3; i < 6; i++) {
            assertEquals(IngestOutcome.rejected("invalid id"), items.get(i).rejection());
        }
    }

    @Test
    void rejectsEmptyAndOversizedBatches() {
        assertThrows(RuntimeException.class, () -> parse(
                "{\"deviceId\":\"phone\",\"positions\":[]}"));
        assertThrows(RuntimeException.class, () -> parse(
                "{\"deviceId\":\"phone\",\"positions\":[" + "{},".repeat(500) + "{}]}"));
    }

    @Test
    void settlesNonObjectItemsInsteadOfFailingTheBatch() {
        var items = parse("{\"deviceId\":\"phone\",\"positions\":[1,{\"timestamp\":1700000000,\"lat\":1,\"lon\":2}]}")
                .items();
        assertEquals(IngestOutcome.rejected("invalid"), items.get(0).rejection());
        assertNotNull(items.get(1).position());
    }

    @Test
    void limitsBatchesPerDeviceUnlessDisabled() {
        for (int i = 0; i < 30; i++) {
            assertTrue(OsmAndPositionBatch.allow(-1, 30));
        }
        assertFalse(OsmAndPositionBatch.allow(-1, 30));
        assertTrue(OsmAndPositionBatch.allow(-2, 30));
        for (int i = 0; i < 100; i++) {
            assertTrue(OsmAndPositionBatch.allow(-3, 0));
        }
    }

    @Test
    void parsesUtcAndEpochUnits() {
        assertEquals(Instant.ofEpochSecond(1700000000), OsmAndPositionBatch.timestamp(
                jakarta.json.Json.createValue("2023-11-14T22:13:20")));
        assertEquals(Instant.ofEpochSecond(1700000000), OsmAndPositionBatch.timestamp(
                jakarta.json.Json.createValue(1700000000000L)));
    }
}
