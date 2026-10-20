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

import io.netty.util.Timeout;
import io.netty.util.Timer;
import io.netty.util.TimerTask;
import jakarta.annotation.Nullable;
import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;
import org.traccar.database.OutboxStore;
import org.traccar.forward.PositionData;
import org.traccar.forward.PositionForwarder;
import org.traccar.model.Device;
import org.traccar.model.Position;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;
import org.traccar.storage.Transaction;
import org.traccar.storage.query.Columns;
import org.traccar.storage.query.Condition;
import org.traccar.storage.query.Request;
import org.traccar.session.cache.CacheManager;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

public class PositionForwardingHandler extends BasePositionHandler {

    private static final Logger LOGGER = LoggerFactory.getLogger(PositionForwardingHandler.class);

    private final CacheManager cacheManager;
    private final Timer timer;

    private final PositionForwarder positionForwarder;

    private final boolean retryEnabled;
    private final int retryDelay;
    private final int retryCount;
    private final int retryLimit;

    private final AtomicInteger deliveryPending;

    private final Storage storage;
    private final OutboxStore outbox;

    @Inject
    public PositionForwardingHandler(
            Config config, CacheManager cacheManager, Timer timer, @Nullable PositionForwarder positionForwarder,
            Storage storage, OutboxStore outbox) {

        this.storage = storage;
        this.outbox = outbox;
        this.cacheManager = cacheManager;
        this.timer = timer;
        this.positionForwarder = positionForwarder;

        this.retryEnabled = config.getBoolean(Keys.FORWARD_RETRY_ENABLE);
        this.retryDelay = config.getInteger(Keys.FORWARD_RETRY_DELAY);
        this.retryCount = config.getInteger(Keys.FORWARD_RETRY_COUNT);
        this.retryLimit = config.getInteger(Keys.FORWARD_RETRY_LIMIT);

        this.deliveryPending = new AtomicInteger();
    }

    class AsyncRequestAndCallback implements TimerTask {

        private final PositionData positionData;

        private int retries = 0;

        AsyncRequestAndCallback(PositionData positionData) {
            this.positionData = positionData;
            deliveryPending.incrementAndGet();
        }

        private void send() {
            positionForwarder.forward(positionData).whenComplete((result, throwable) -> {
                if (throwable == null) {
                    deliveryPending.decrementAndGet();
                } else {
                    retry(throwable);
                }
            });
        }

        private void retry(Throwable throwable) {
            boolean scheduled = false;
            try {
                if (retryEnabled && deliveryPending.get() <= retryLimit && retries < retryCount) {
                    schedule();
                    scheduled = true;
                }
            } finally {
                int pending = scheduled ? deliveryPending.get() : deliveryPending.decrementAndGet();
                LOGGER.warn("Position forwarding failed: {} pending", pending, throwable);
            }
        }

        private void schedule() {
            timer.newTimeout(this, retryDelay * (1L << retries++), TimeUnit.MILLISECONDS);
        }

        @Override
        public void run(Timeout timeout) {
            boolean sent = false;
            try {
                if (!timeout.isCancelled()) {
                    send();
                    sent = true;
                }
            } finally {
                if (!sent) {
                    deliveryPending.decrementAndGet();
                }
            }
        }
    }

    @Override
    public void onPosition(Position position, Callback callback) {
        if (positionForwarder != null) {
            PositionData positionData = new PositionData();
            positionData.setPosition(position);
            positionData.setDevice(cacheManager.getObject(Device.class, position.getDeviceId()));
            Transaction transaction = storage.current();
            if (transaction != null) {
                // Forward only what was committed, once per stored position, and retry until delivered
                try {
                    var entry = outbox.add(OutboxStore.POSITION, position.getDeviceId(), position.getId());
                    transaction.afterCommit(() -> deliver(entry, positionData));
                } catch (StorageException e) {
                    LOGGER.warn("Position forwarding queueing failed", e);
                }
            } else {
                new AsyncRequestAndCallback(positionData).send();
            }
        }
        callback.processed(false);
    }

    /** Delivers an entry right after its commit, if a delivery slot is free; otherwise the outbox task will. */
    private void deliver(OutboxStore.Entry entry, PositionData positionData) {
        deliver(entry, positionData, 0);
    }

    private boolean deliver(OutboxStore.Entry entry, PositionData positionData, long waitMillis) {
        OutboxStore.Entry claimed;
        try {
            claimed = outbox.start(entry, waitMillis);
        } catch (StorageException e) {
            LOGGER.warn("Position forwarding claim failed", e);
            return false;
        }
        if (claimed == null) {
            return false;
        }
        CompletableFuture<Void> result;
        try {
            result = positionForwarder.forward(positionData);
        } catch (RuntimeException e) {
            result = CompletableFuture.failedFuture(e);
        }
        result.whenComplete((ignore, throwable) -> {
            if (throwable != null) {
                LOGGER.warn("Position forwarding failed, will retry", throwable);
            }
            outbox.finish(claimed, throwable == null);
        });
        return true;
    }

    /**
     * Delivers an outbox entry left behind by a crash, a failed attempt or a full set of delivery slots,
     * waiting up to {@code waitMillis} for a slot. Returns whether a delivery started.
     */
    public boolean redeliver(OutboxStore.Entry entry, long waitMillis) throws StorageException {
        Position position = storage.getObject(Position.class, new Request(
                new Columns.All(), new Condition.Equals("id", entry.objectId())));
        if (position == null || positionForwarder == null) {
            OutboxStore.Entry claimed = outbox.start(entry, waitMillis);
            if (claimed != null) {
                outbox.finish(claimed, true); // nothing to deliver
            }
            return claimed != null;
        }
        PositionData positionData = new PositionData();
        positionData.setPosition(position);
        Device device = cacheManager.getObject(Device.class, position.getDeviceId());
        positionData.setDevice(device != null ? device : storage.getObject(Device.class, new Request(
                new Columns.All(), new Condition.Equals("id", position.getDeviceId()))));
        return deliver(entry, positionData, waitMillis);
    }

}
