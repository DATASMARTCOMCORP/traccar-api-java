package org.traccar.integration;

import org.junit.jupiter.api.Test;

import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Timestamp;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.stream.IntStream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The operational indicator for late data (docs/late-data-incomplete.sql), run as written against the tables
 * real ingestion fills.
 */
class LateDataVisibilityTest extends IngestionTestSupport {

    private List<String> items(int from, int to) {
        return IntStream.range(from, to).mapToObj(k -> routeItem(k, 30)).toList();
    }

    private record Row(String uniqueId, Timestamp from, Timestamp to, long late, long geofenceEvents) { }

    private List<Row> indicator() throws Exception {
        String sql = Files.readAllLines(Path.of("docs/late-data-incomplete.sql")).stream()
                .filter(line -> !line.startsWith("--"))
                .collect(java.util.stream.Collectors.joining("\n"));
        String query = sql.substring(0, sql.indexOf(';')); // the indicator; the second query is commented out
        var rows = new ArrayList<Row>();
        try (var connection = connect(); var rs = connection.createStatement().executeQuery(query)) {
            while (rs.next()) {
                rows.add(new Row(rs.getString("uniqueid"), rs.getTimestamp("fromtime"), rs.getTimestamp("totime"),
                        rs.getLong("latepositions"), rs.getLong("lategeofenceevents")));
            }
        }
        return rows;
    }

    private boolean reportAffected(long deviceId, int fromPoint, int toPoint) throws Exception {
        try (var connection = connect(); var statement = connection.prepareStatement(
                "SELECT EXISTS (SELECT 1 FROM tc_late_positions WHERE deviceid = ? AND completedat IS NULL"
                        + " AND fromtime <= ? AND totime >= ?)")) {
            statement.setLong(1, deviceId);
            statement.setTimestamp(2, Timestamp.from(time(toPoint)));
            statement.setTimestamp(3, Timestamp.from(time(fromPoint)));
            try (var rs = statement.executeQuery()) {
                rs.next();
                return rs.getBoolean(1);
            }
        }
    }

    @Test
    void lateIntervalsAndTheirDerivedEventsAreListedUntilReconstructed() throws Exception {
        var server = start();
        var late = seed(server, "late-device", Map.of());
        var ordered = seed(server, "ordered-device", Map.of());

        server.batch(batch(ordered, items(0, 21)));
        server.batch(batch(late, items(18, 27)));
        server.batch(batch(late, items(9, 18))); // the gap arrives afterwards, crossing the geofence

        var rows = indicator();
        assertEquals(1, rows.size(), rows.toString());
        var row = rows.getFirst();
        assertEquals("late-device", row.uniqueId());
        assertEquals(time(9), row.from().toInstant());
        assertEquals(time(18), row.to().toInstant());
        assertEquals(9, row.late());
        assertEquals(2, row.geofenceEvents(), "the visit reported late: enter and exit");

        assertTrue(reportAffected(late.deviceId(), 0, 12));
        assertFalse(reportAffected(late.deviceId(), 19, 26));
        assertFalse(reportAffected(ordered.deviceId(), 0, 26));

        // What a reconstruction job will do when it finishes an interval
        try (var connection = connect()) {
            connection.createStatement().execute("UPDATE tc_late_positions SET completedat = now()");
        }
        assertEquals(List.of(), indicator());
    }

}
