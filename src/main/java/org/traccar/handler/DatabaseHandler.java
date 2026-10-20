/*
 * Copyright 2015 - 2026 Anton Tananaev (anton@traccar.org)
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
import org.traccar.database.IngestOutcome;
import org.traccar.database.PositionBatchWriter;
import org.traccar.database.StatisticsManager;
import org.traccar.model.Position;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;
import org.traccar.storage.StoredPosition;
import org.traccar.storage.Transaction;
import org.traccar.storage.query.Columns;
import org.traccar.storage.query.Request;

public class DatabaseHandler extends BasePositionHandler {

    private static final Logger LOGGER = LoggerFactory.getLogger(DatabaseHandler.class);

    private final PositionBatchWriter batchWriter;
    private final StatisticsManager statisticsManager;
    private final Storage storage;

    @Inject
    public DatabaseHandler(PositionBatchWriter batchWriter, StatisticsManager statisticsManager, Storage storage) {
        this.batchWriter = batchWriter;
        this.statisticsManager = statisticsManager;
        this.storage = storage;
    }

    @Override
    public void onPosition(Position position, Callback callback) {
        var ticket = position.getIngestTicket();
        Transaction transaction = storage.current();
        if (ticket != null || transaction != null) {
            // In the position's transaction: the insert commits with everything else, or not at all
            try {
                if (ticket != null) {
                    StoredPosition stored = storage.addPosition(position, ticket.getEventId());
                    if (stored.duplicate()) {
                        ticket.decide(IngestOutcome.duplicate(stored.positionId()));
                        callback.processed(true);
                        return;
                    }
                    stored(position, stored.positionId());
                } else {
                    stored(position, storage.addObject(position, new Request(new Columns.Exclude("id"))));
                }
                callback.processed(false);
            } catch (StorageException error) {
                failed(position, error, callback);
            }
            return;
        }
        batchWriter.submit(position).whenComplete((id, error) -> {
            if (error == null) {
                stored(position, id);
                callback.processed(false);
            } else {
                failed(position, error, callback);
            }
        });
    }

    private void stored(Position position, long id) {
        position.setId(id);
        statisticsManager.registerMessageStored(position.getDeviceId(), position.getProtocol());
    }

    // A position that is not stored must not produce events or become the device's latest position.
    private void failed(Position position, Throwable error, Callback callback) {
        LOGGER.warn("Failed to store position", error);
        if (position.getIngestTicket() != null) {
            position.getIngestTicket().decide(IngestOutcome.failed("storage"));
        }
        Transaction transaction = storage.current();
        if (transaction != null) {
            transaction.setRollbackOnly();
        }
        callback.processed(true);
    }

}
