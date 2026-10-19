/*
 * Copyright 2022 - 2025 Anton Tananaev (anton@traccar.org)
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
package org.traccar.storage;

import org.traccar.model.BaseModel;
import org.traccar.model.Device;
import org.traccar.model.Permission;
import org.traccar.model.Position;
import org.traccar.storage.query.Columns;
import org.traccar.storage.query.Condition;
import org.traccar.storage.query.Request;

import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.stream.Stream;

public abstract class Storage {

    public static final int MAX_GROUP_DEPTH = 3;

    public abstract <T> List<T> getObjects(Class<T> clazz, Request request) throws StorageException;

    public abstract <T> Stream<T> getObjectsStream(Class<T> clazz, Request request) throws StorageException;

    public abstract <T> long addObject(T entity, Request request) throws StorageException;

    public <T> List<Long> addObjects(List<T> entities, Request request) throws StorageException {
        List<Long> ids = new ArrayList<>(entities.size());
        for (T entity : entities) {
            ids.add(addObject(entity, request));
        }
        return ids;
    }

    public abstract <T> void updateObject(T entity, Request request) throws StorageException;

    public abstract void removeObject(Class<?> clazz, Request request) throws StorageException;

    public abstract List<Permission> getPermissions(
            Class<? extends BaseModel> ownerClass, long ownerId,
            Class<? extends BaseModel> propertyClass, long propertyId) throws StorageException;

    public abstract void addPermission(Permission permission) throws StorageException;

    public abstract void removePermission(Permission permission) throws StorageException;

    public List<Permission> getPermissions(
            Class<? extends BaseModel> ownerClass,
            Class<? extends BaseModel> propertyClass) throws StorageException {
        return getPermissions(ownerClass, 0, propertyClass, 0);
    }

    public <T> T getObject(Class<T> clazz, Request request) throws StorageException {
        try (var objects = getObjectsStream(clazz, request)) {
            return objects.findFirst().orElse(null);
        }
    }

    /**
     * Stores a position together with the receipt for its report id, atomically. If the report was already
     * stored, nothing is written and the earlier row is returned as a duplicate.
     */
    public StoredPosition addPosition(Position position, String eventId) throws StorageException {
        return new StoredPosition(addObject(position, new Request(new Columns.Exclude("id"))), false);
    }

    /**
     * Returns the position stored for a report id, or 0 if there is none.
     */
    public long getReceipt(long deviceId, String eventId) throws StorageException {
        return 0;
    }

    /**
     * Deletes receipts of reports with a fix time before the given one.
     */
    public void removeReceipts(Date fixTime) throws StorageException {
    }

    /**
     * Makes the position the device's latest unless the stored latest is newer. Positions are ordered by fix
     * time, then by id. Returns whether the device was updated.
     */
    public boolean updateLatestPosition(long deviceId, long positionId, Date fixTime) throws StorageException {
        Device device = new Device();
        device.setId(deviceId);
        device.setPositionId(positionId);
        updateObject(device, new Request(
                new Columns.Include("positionId"),
                new Condition.Equals("id", deviceId)));
        return true;
    }

    /**
     * Starts the transaction that processes one position of a device. It holds the device's lock until it
     * finishes, so positions of one device are processed one at a time across all servers.
     */
    public Transaction begin(long deviceId) throws StorageException {
        return new Transaction();
    }

    /** The transaction bound to the current thread, or null. */
    public Transaction current() {
        return null;
    }

    /**
     * Runs SQL on the connection of the current transaction, or on its own auto-commit connection.
     */
    public <R> R execute(SqlWork<R> work) throws StorageException {
        throw new StorageException("SQL is not supported by this storage");
    }

}
