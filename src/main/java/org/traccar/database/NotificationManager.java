/*
 * Copyright 2016 - 2026 Anton Tananaev (anton@traccar.org)
 * Copyright 2016 - 2018 Andrey Kunitsyn (andrey@traccar.org)
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
package org.traccar.database;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.forward.EventData;
import org.traccar.forward.EventForwarder;
import org.traccar.geocoder.Geocoder;
import org.traccar.helper.DateUtil;
import org.traccar.model.Calendar;
import org.traccar.model.Device;
import org.traccar.model.Event;
import org.traccar.model.Geofence;
import org.traccar.model.Maintenance;
import org.traccar.model.Position;
import org.traccar.notification.NotificatorManager;
import org.traccar.session.cache.CacheManager;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;
import org.traccar.storage.Transaction;
import org.traccar.storage.query.Columns;
import org.traccar.storage.query.Condition;
import org.traccar.storage.query.Request;

import jakarta.annotation.Nullable;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashSet;
import java.util.Map;
import java.util.Map.Entry;
import java.util.Set;
import java.util.concurrent.CompletableFuture;

@Singleton
public class NotificationManager {

    private static final Logger LOGGER = LoggerFactory.getLogger(NotificationManager.class);

    private final Storage storage;
    private final OutboxStore outbox;
    private final CacheManager cacheManager;
    private final EventForwarder eventForwarder;
    private final NotificatorManager notificatorManager;
    private final Geocoder geocoder;

    private final boolean geocodeOnRequest;
    private final long timeThreshold;
    private final Set<Long> blockedUsers = new HashSet<>();

    @Inject
    public NotificationManager(
            Config config, Storage storage, OutboxStore outbox, CacheManager cacheManager,
            @Nullable EventForwarder eventForwarder,
            NotificatorManager notificatorManager, @Nullable Geocoder geocoder) {
        this.storage = storage;
        this.outbox = outbox;
        this.cacheManager = cacheManager;
        this.eventForwarder = eventForwarder;
        this.notificatorManager = notificatorManager;
        this.geocoder = geocoder;
        geocodeOnRequest = config.getBoolean(Keys.GEOCODER_ON_REQUEST);
        timeThreshold = config.getLong(Keys.NOTIFICATOR_TIME_THRESHOLD);
        String blockedUsersString = config.getString(Keys.NOTIFICATION_BLOCK_USERS);
        if (blockedUsersString != null) {
            for (String userIdString : blockedUsersString.split(",")) {
                blockedUsers.add(Long.parseLong(userIdString));
            }
        }
    }

    private static final long DELIVERY_LEASE = 60_000;

    private void updateEvent(Event event, Position position) {
        try {
            event.setId(storage.addObject(event, new Request(new Columns.Exclude("id"))));
        } catch (StorageException error) {
            LOGGER.warn("Event save error", error);
        }

        Transaction transaction = storage.current();
        if (transaction != null) {
            // Deliveries are stored with the event and leave only after the commit, at least once
            try {
                var entries = new ArrayList<OutboxStore.Entry>();
                if (eventForwarder != null) {
                    entries.add(outbox.add(OutboxStore.EVENT, event.getDeviceId(), event.getId()));
                }
                entries.add(outbox.add(OutboxStore.NOTIFY, event.getDeviceId(), event.getId()));
                transaction.afterCommit(() -> entries.forEach(entry -> deliver(entry, event, position)));
            } catch (StorageException error) {
                LOGGER.warn("Event delivery queueing error", error);
            }
            return;
        }

        forwardEvent(event, position);
        sendNotifications(event, position);
    }

    private void deliver(OutboxStore.Entry entry, Event event, Position position) {
        try {
            if (!outbox.claim(entry, DELIVERY_LEASE)) {
                return;
            }
        } catch (StorageException error) {
            LOGGER.warn("Event delivery claim error", error);
            return;
        }
        var key = new Object();
        CompletableFuture<Void> result;
        try {
            cacheManager.addDevice(event.getDeviceId(), key);
            result = entry.kind().equals(OutboxStore.EVENT)
                    ? forwardEvent(event, position)
                    : sendNotifications(event, position);
        } catch (Exception error) {
            result = CompletableFuture.failedFuture(error);
        } finally {
            cacheManager.removeDevice(event.getDeviceId(), key);
        }
        result.whenComplete((ignore, error) -> {
            try {
                if (error == null) {
                    outbox.delivered(entry);
                } else {
                    LOGGER.warn("Event delivery failed, will retry", error);
                    outbox.retryLater(entry);
                }
            } catch (StorageException storageError) {
                LOGGER.warn("Event delivery update error", storageError);
            }
        });
    }

    /** Delivers an outbox entry left behind by a crash or a failed attempt. */
    public void redeliver(OutboxStore.Entry entry) throws StorageException {
        Event event = storage.getObject(Event.class, new Request(
                new Columns.All(), new Condition.Equals("id", entry.objectId())));
        if (event == null) {
            outbox.delivered(entry);
            return;
        }
        Position position = event.getPositionId() > 0 ? storage.getObject(Position.class, new Request(
                new Columns.All(), new Condition.Equals("id", event.getPositionId()))) : null;
        deliver(entry, event, position);
    }

    private CompletableFuture<Void> sendNotifications(Event event, Position position) {
        if (System.currentTimeMillis() - event.getEventTime().getTime() > timeThreshold) {
            LOGGER.info("Skipping notifications for old event");
            return CompletableFuture.completedFuture(null);
        }

        var notifications = cacheManager.getDeviceNotifications(event.getDeviceId()).stream()
                .filter(notification -> notification.getType().equals(event.getType()))
                .filter(notification -> {
                    if (event.getType().equals(Event.TYPE_ALARM)) {
                        String alarmsAttribute = notification.getString("alarms");
                        if (alarmsAttribute != null) {
                            return Arrays.asList(alarmsAttribute.split(","))
                                    .contains(event.getString(Position.KEY_ALARM));
                        }
                        return false;
                    }
                    return true;
                })
                .filter(notification -> {
                    String geofenceIdsString = notification.getString("geofenceIds");
                    if (geofenceIdsString != null) {
                        return Arrays.stream(geofenceIdsString.split(",")).map(Long::parseLong)
                                .anyMatch(geofenceId -> geofenceId == event.getGeofenceId());
                    }
                    return true;
                })
                .filter(notification -> {
                    long calendarId = notification.getCalendarId();
                    Calendar calendar = calendarId != 0 ? cacheManager.getObject(Calendar.class, calendarId) : null;
                    return calendar == null || calendar.checkMoment(event.getEventTime());
                })
                .toList();

        Device device = cacheManager.getObject(Device.class, event.getDeviceId());
        LOGGER.info(
                "Event id: {}, time: {}, type: {}, notifications: {}",
                device.getUniqueId(),
                DateUtil.formatDate(event.getEventTime(), false),
                event.getType(),
                notifications.size());

        var sent = new ArrayList<CompletableFuture<Void>>();
        if (!notifications.isEmpty()) {
            if (position != null && position.getAddress() == null && geocodeOnRequest && geocoder != null) {
                position.setAddress(geocoder.getAddress(position.getLatitude(), position.getLongitude(), null));
            }

            notifications.forEach(notification -> {
                cacheManager.getNotificationUsers(notification.getId(), event.getDeviceId()).forEach(user -> {
                    if (blockedUsers.contains(user.getId())) {
                        LOGGER.info("User {} notification blocked", user.getId());
                        return;
                    }
                    for (String notificator : notification.getNotificatorsTypes()) {
                        sent.add(notificatorManager.getNotificator(notificator)
                                .sendAsync(notification, user, event, position)
                                .exceptionally(throwable -> {
                                    LOGGER.warn("Notification failed", throwable);
                                    return null;
                                }));
                    }
                });
            });
        }
        // A failed notificator is logged, not retried: retrying would repeat the ones that succeeded
        return CompletableFuture.allOf(sent.toArray(CompletableFuture[]::new));
    }

    private CompletableFuture<Void> forwardEvent(Event event, Position position) {
        if (eventForwarder != null) {
            EventData eventData = new EventData();
            eventData.setEvent(event);
            eventData.setPosition(position);
            eventData.setDevice(cacheManager.getObject(Device.class, event.getDeviceId()));
            if (event.getGeofenceId() != 0) {
                eventData.setGeofence(cacheManager.getObject(Geofence.class, event.getGeofenceId()));
            }
            if (event.getMaintenanceId() != 0) {
                eventData.setMaintenance(cacheManager.getObject(Maintenance.class, event.getMaintenanceId()));
            }
            CompletableFuture<Void> result = eventForwarder.forward(eventData);
            result.exceptionally(throwable -> {
                LOGGER.warn("Event forwarding failed", throwable);
                return null;
            });
            return result;
        }
        return CompletableFuture.completedFuture(null);
    }

    public void updateEvents(Map<Event, Position> events) {
        for (Entry<Event, Position> entry : events.entrySet()) {
            Event event = entry.getKey();
            Position position = entry.getValue();
            var key = new Object();
            try {
                cacheManager.addDevice(event.getDeviceId(), key);
                updateEvent(event, position);
            } catch (Exception e) {
                throw new RuntimeException(e);
            } finally {
                cacheManager.removeDevice(event.getDeviceId(), key);
            }
        }
    }
}
