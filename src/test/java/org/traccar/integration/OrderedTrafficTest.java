package org.traccar.integration;

import org.junit.jupiter.api.Test;
import org.traccar.helper.DistanceCalculator;
import org.traccar.model.Event;
import org.traccar.model.Position;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Characterization of ordinary, chronologically ordered live traffic over the single-position OsmAnd endpoint,
 * the way the mobile clients and TrackHub send it today. These expectations were checked against the server
 * before the ingestion changes and must keep holding after them.
 */
class OrderedTrafficTest extends IngestionTestSupport {

    // 21 moving points crossing the geofence (point 10 is inside), then 6 stationary points at the end
    private static final int MOVING = 21;
    private static final int TOTAL = 27;

    private void drive(TraccarInstance server, Fixture fixture) throws Exception {
        for (int k = 0; k < TOTAL; k++) {
            int point = Math.min(k, MOVING - 1);
            String query = "id=" + fixture.uniqueId() + "&lat=" + routeLat(point) + "&lon=" + CENTER_LON
                    + "&timestamp=" + time(k) + "&speed=" + (k < MOVING ? 30 : 0) + "&batt=80";
            assertEquals(200, server.live(query).statusCode());
        }
    }

    @Test
    void orderedLiveTrafficProducesHistoryCurrentStateAndEvents() throws Exception {
        var server = startSynchronous();
        var fixture = seed(server, "ordered", Map.of("speedLimit", 25.0));
        server.watch(fixture.userId());

        drive(server, fixture);

        var stored = positions(fixture.deviceId());
        assertEquals(TOTAL, stored.size());
        for (int k = 0; k < TOTAL; k++) {
            assertEquals(time(k), stored.get(k).fixTime());
        }
        assertEquals(stored.getLast().id(), latestPositionId(fixture.deviceId()));

        // Distance accumulates along the route and stays put while parked
        for (int k = 1; k < TOTAL; k++) {
            assertTrue(stored.get(k).totalDistance() >= stored.get(k - 1).totalDistance());
        }
        double route = 0;
        for (int k = 1; k < MOVING; k++) {
            route += DistanceCalculator.distance(routeLat(k - 1), CENTER_LON, routeLat(k), CENTER_LON);
        }
        assertEquals(route, stored.getLast().totalDistance(), 0.01);
        assertEquals(0.0, stored.getLast().distance(), 0.01);

        // Every position reached WebSocket clients, in order
        List<Position> pushed = server.socketPositions;
        assertEquals(TOTAL, pushed.size());
        for (int k = 0; k < TOTAL; k++) {
            assertEquals(time(k), pushed.get(k).getFixTime().toInstant());
        }

        assertEquals(List.of(
                "geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(11)),
                eventTrail(fixture.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(List.of("deviceOverspeed@" + routeLat(0)),
                eventTrail(fixture.deviceId(), Event.TYPE_DEVICE_OVERSPEED));
        var motion = eventTrail(fixture.deviceId(), Event.TYPE_DEVICE_MOVING, Event.TYPE_DEVICE_STOPPED);
        assertEquals(2, motion.size(), motion.toString());
        assertTrue(motion.get(0).startsWith(Event.TYPE_DEVICE_MOVING));
        assertEquals("deviceStopped@" + routeLat(MOVING - 1), motion.get(1));

        // Each stored event was notified once over the WebSocket and forwarded once to the webhook
        var events = events(fixture.deviceId());
        assertEquals(events.stream().map(EventRow::type).toList(),
                server.socketEvents.stream().map(Event::getType).toList());
        awaitForwarded(events.size());
        assertEquals(events.size(), forwarded.size());
        assertTrue(events.stream().noneMatch(EventRow::late));
    }

    @Test
    void validationAndFiltersOfSingleRequests() throws Exception {
        var server = startSynchronous();
        var fixture = seed(server, "validation", Map.of());

        assertEquals(400, server.live("id=unknown-device&lat=1&lon=1").statusCode());
        assertEquals(400, server.live("lat=1&lon=1").statusCode());
        assertEquals(200, server.live("id=validation&lat=0&lon=0&timestamp=" + time(0)).statusCode());
        assertEquals(0, positions(fixture.deviceId()).size(), "filter.zero drops (0, 0)");

        assertEquals(200, server.live(liveQuery(fixture, 1, 10)).statusCode());
        assertEquals(1, positions(fixture.deviceId()).size());
    }

    @Test
    void devicesDoNotShareState() throws Exception {
        var server = startSynchronous();
        var first = seed(server, "first", Map.of());
        var second = seed(server, "second", Map.of());

        for (int k = 8; k <= 12; k++) {
            server.live(liveQuery(first, k, 30));
            server.live("id=second&lat=" + routeLat(0) + "&lon=" + CENTER_LON + "&timestamp=" + time(k)
                    + "&speed=0");
        }

        assertEquals(5, positions(first.deviceId()).size());
        assertEquals(5, positions(second.deviceId()).size());
        assertEquals(List.of("geofenceEnter@" + routeLat(10), "geofenceExit@" + routeLat(11)),
                eventTrail(first.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(List.of(), eventTrail(second.deviceId(), Event.TYPE_GEOFENCE_ENTER, Event.TYPE_GEOFENCE_EXIT));
        assertEquals(0.0, positions(second.deviceId()).getLast().totalDistance(), 0.01);
    }

}
