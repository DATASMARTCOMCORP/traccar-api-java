/*
 * Copyright 2016 - 2026 Anton Tananaev (anton@traccar.org)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package org.traccar.handler.events;

import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.helper.model.PositionUtil;
import org.traccar.model.Calendar;
import org.traccar.model.Event;
import org.traccar.model.Geofence;
import org.traccar.model.Position;
import org.traccar.session.cache.CacheManager;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;

import java.util.HashSet;
import java.util.Set;
import java.util.TreeSet;

public class GeofenceEventHandler extends BaseEventHandler {

    private static final Logger LOGGER = LoggerFactory.getLogger(GeofenceEventHandler.class);

    private final CacheManager cacheManager;
    private final Storage storage;

    @Inject
    public GeofenceEventHandler(CacheManager cacheManager, Storage storage) {
        this.cacheManager = cacheManager;
        this.storage = storage;
    }

    @Override
    public void onPosition(Position position, Callback callback) {
        if (!PositionUtil.isLatest(cacheManager, position)) {
            try {
                onLatePosition(position, callback);
            } catch (StorageException e) {
                LOGGER.warn("Failed to load neighbor positions", e);
            }
            return;
        }

        Set<Long> oldGeofences = new HashSet<>();
        Position lastPosition = cacheManager.getPosition(position.getDeviceId());
        if (lastPosition != null && lastPosition.getGeofenceIds() != null) {
            oldGeofences.addAll(lastPosition.getGeofenceIds());
        }

        Set<Long> newGeofences = new HashSet<>();
        if (position.getGeofenceIds() != null) {
            newGeofences.addAll(position.getGeofenceIds());
            newGeofences.removeAll(oldGeofences);
            position.getGeofenceIds().forEach(oldGeofences::remove);
        }

        for (long geofenceId : oldGeofences) {
            detected(Event.TYPE_GEOFENCE_EXIT, geofenceId, position, false, callback);
        }
        for (long geofenceId : newGeofences) {
            detected(Event.TYPE_GEOFENCE_ENTER, geofenceId, position, false, callback);
        }
    }

    /**
     * A late position falls between two stored positions, previous and next. Events for the step from previous
     * to next were already decided when next arrived. If the late position is on the other side of a geofence
     * boundary than both of them, the device went in and out (or out and in) without anyone noticing: report the
     * crossing at the late position and the crossing back at next. When previous and next differ, the change
     * was already reported at next and nothing is added.
     */
    private void onLatePosition(Position position, Callback callback) throws StorageException {
        Position next = PositionUtil.getNeighbor(storage, position, false);
        if (next == null) {
            return;
        }
        Position previous = PositionUtil.getNeighbor(storage, position, true);
        Set<Long> before = geofences(previous);
        Set<Long> after = geofences(next);
        Set<Long> current = geofences(position);

        Set<Long> candidates = new TreeSet<>(current);
        candidates.addAll(before);
        for (long geofenceId : candidates) {
            boolean inside = current.contains(geofenceId);
            if (before.contains(geofenceId) == after.contains(geofenceId) && before.contains(geofenceId) != inside) {
                String type = inside ? Event.TYPE_GEOFENCE_ENTER : Event.TYPE_GEOFENCE_EXIT;
                String backType = inside ? Event.TYPE_GEOFENCE_EXIT : Event.TYPE_GEOFENCE_ENTER;
                detected(type, geofenceId, position, true, callback);
                detected(backType, geofenceId, next, true, callback);
            }
        }
    }

    private static Set<Long> geofences(Position position) {
        if (position == null || position.getGeofenceIds() == null) {
            return Set.of();
        }
        return new HashSet<>(position.getGeofenceIds());
    }

    private void detected(String type, long geofenceId, Position position, boolean late, Callback callback) {
        Geofence geofence = cacheManager.getObject(Geofence.class, geofenceId);
        if (geofence == null) {
            return;
        }
        long calendarId = geofence.getCalendarId();
        Calendar calendar = calendarId != 0 ? cacheManager.getObject(Calendar.class, calendarId) : null;
        if (calendar == null || calendar.checkMoment(position.getFixTime())) {
            Event event = new Event(type, position);
            event.setGeofenceId(geofenceId);
            if (late) {
                event.set(Event.KEY_LATE, true);
                callback.eventDetected(event, position);
            } else {
                callback.eventDetected(event);
            }
        }
    }

}
