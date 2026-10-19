package org.traccar.protocol;

import org.junit.jupiter.api.Test;
import java.time.Instant;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

class OsmAndPositionBatchTest {

    @Test
    void sortsDeduplicatesAndSettlesInvalidFixes() {
        var batch = OsmAndPositionBatch.parse("""
                {"deviceId":" phone ","installId":"install","os":"android","positions":[
                  {"timestamp":1700000010,"lat":-22,"lon":-43,"speed":12,"batt":80,"is_moving":true},
                  {"timestamp":1700000000000,"lat":-23,"lon":-44},
                  {"timestamp":"2023-11-14T22:13:20Z","lat":-23,"lon":-44},
                  {"timestamp":1700000000,"lat":91,"lon":0},
                  {"timestamp":"invalid","lat":0,"lon":0}
                ]}
                """);
        assertEquals("phone", batch.deviceId());
        assertEquals(5, batch.accepted());
        assertEquals(2, batch.positions().size());
        assertEquals(Instant.ofEpochSecond(1700000000), batch.positions().getFirst().getFixTime().toInstant());
        var latest = batch.positions().getLast();
        assertEquals(12, latest.getSpeed());
        assertEquals(80.0, latest.getAttributes().get("batteryLevel"));
        assertEquals(true, latest.getAttributes().get("is_moving"));
        assertEquals("install", latest.getAttributes().get("installId"));
    }

    @Test
    void rejectsEmptyAndOversizedBatches() {
        assertThrows(RuntimeException.class, () -> OsmAndPositionBatch.parse(
                "{\"deviceId\":\"phone\",\"positions\":[]}"));
        assertThrows(RuntimeException.class, () -> OsmAndPositionBatch.parse(
                "{\"deviceId\":\"phone\",\"positions\":[" + "{},".repeat(500) + "{}]}"));
    }

    @Test
    void parsesUtcAndEpochUnits() {
        assertEquals(Instant.ofEpochSecond(1700000000), OsmAndPositionBatch.timestamp(
                jakarta.json.Json.createValue("2023-11-14T22:13:20")));
        assertEquals(Instant.ofEpochSecond(1700000000), OsmAndPositionBatch.timestamp(
                jakarta.json.Json.createValue(1700000000000L)));
    }
}
