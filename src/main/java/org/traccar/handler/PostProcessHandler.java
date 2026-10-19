/*
 * Copyright 2024 Anton Tananaev (anton@traccar.org)
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
package org.traccar.handler;

import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.helper.model.PositionUtil;
import org.traccar.model.Position;
import org.traccar.session.ConnectionManager;
import org.traccar.session.cache.CacheManager;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;
import org.traccar.storage.Transaction;

import java.sql.PreparedStatement;
import java.sql.Timestamp;

public class PostProcessHandler extends BasePositionHandler {

    private static final Logger LOGGER = LoggerFactory.getLogger(PostProcessHandler.class);

    private final CacheManager cacheManager;
    private final Storage storage;
    private final ConnectionManager connectionManager;

    @Inject
    public PostProcessHandler(CacheManager cacheManager, Storage storage, ConnectionManager connectionManager) {
        this.cacheManager = cacheManager;
        this.storage = storage;
        this.connectionManager = connectionManager;
    }

    @Override
    public void onPosition(Position position, Callback callback) {
        try {
            // The cache check is per process; the storage check also holds when other servers or workers
            // write positions of the same device.
            if (PositionUtil.isLatest(cacheManager, position) && storage.updateLatestPosition(
                    position.getDeviceId(), position.getId(), position.getFixTime())) {
                cacheManager.updatePosition(position);
                Transaction transaction = storage.current();
                if (transaction != null) {
                    transaction.afterCommit(() -> connectionManager.updatePosition(true, position));
                } else {
                    connectionManager.updatePosition(true, position);
                }
            } else if (storage.current() != null) {
                recordLateGap(position);
            }
        } catch (StorageException error) {
            LOGGER.warn("Failed to update device", error);
        }
        callback.processed(false);
    }

    /**
     * A late position changes history that derived results (trips, motion, overspeed, ignition events) were
     * computed from. Record the affected interval with the position, durably, so a reconstruction job can
     * redo it; until then those results are incomplete for the interval.
     */
    private void recordLateGap(Position position) throws StorageException {
        Position previous = PositionUtil.getNeighbor(storage, position, true);
        Position next = PositionUtil.getNeighbor(storage, position, false);
        storage.execute(connection -> {
            try (PreparedStatement statement = connection.prepareStatement(
                    "INSERT INTO tc_late_positions (deviceid, positionid, fromtime, totime, createdat)"
                            + " VALUES (?, ?, ?, ?, ?)")) {
                statement.setLong(1, position.getDeviceId());
                statement.setLong(2, position.getId());
                statement.setTimestamp(3, new Timestamp(
                        (previous != null ? previous : position).getFixTime().getTime()));
                statement.setTimestamp(4, new Timestamp((next != null ? next : position).getFixTime().getTime()));
                statement.setTimestamp(5, new Timestamp(System.currentTimeMillis()));
                return statement.executeUpdate();
            }
        });
    }

}
