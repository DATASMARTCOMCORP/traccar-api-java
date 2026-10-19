/*
 * Copyright 2024 - 2025 Anton Tananaev (anton@traccar.org)
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
package org.traccar;

import com.google.inject.Injector;
import io.netty.channel.ChannelHandler;
import io.netty.channel.ChannelHandlerContext;
import io.netty.channel.ChannelInboundHandlerAdapter;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.database.BufferingManager;
import org.traccar.database.IngestOutcome;
import org.traccar.database.NotificationManager;
import org.traccar.handler.BasePositionHandler;
import org.traccar.handler.ComputedAttributesHandler;
import org.traccar.handler.CopyAttributesHandler;
import org.traccar.handler.DatabaseHandler;
import org.traccar.handler.DistanceHandler;
import org.traccar.handler.DriverHandler;
import org.traccar.handler.EngineHoursHandler;
import org.traccar.handler.FilterHandler;
import org.traccar.handler.GeocoderHandler;
import org.traccar.handler.GeofenceHandler;
import org.traccar.handler.GeolocationHandler;
import org.traccar.handler.HemisphereHandler;
import org.traccar.handler.MapMatcherHandler;
import org.traccar.handler.MotionHandler;
import org.traccar.handler.OutdatedHandler;
import org.traccar.handler.PositionForwardingHandler;
import org.traccar.handler.PostProcessHandler;
import org.traccar.handler.ReceiptHandler;
import org.traccar.handler.SpeedLimitHandler;
import org.traccar.handler.TimeHandler;
import org.traccar.handler.events.AlarmEventHandler;
import org.traccar.handler.events.BaseEventHandler;
import org.traccar.handler.events.BehaviorEventHandler;
import org.traccar.handler.events.CommandResultEventHandler;
import org.traccar.handler.events.DriverEventHandler;
import org.traccar.handler.events.FuelEventHandler;
import org.traccar.handler.events.GeofenceEventHandler;
import org.traccar.handler.events.IgnitionEventHandler;
import org.traccar.handler.events.MaintenanceEventHandler;
import org.traccar.handler.events.MediaEventHandler;
import org.traccar.handler.events.MotionEventHandler;
import org.traccar.handler.events.OverspeedEventHandler;
import org.traccar.handler.events.ProximityEventHandler;
import org.traccar.handler.network.AcknowledgementHandler;
import org.traccar.helper.PositionLogger;
import org.traccar.model.Event;
import org.traccar.model.Position;
import org.traccar.session.cache.CacheManager;
import org.traccar.storage.Storage;
import org.traccar.storage.StorageException;
import org.traccar.storage.Transaction;

import java.util.HashMap;
import java.util.LinkedList;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Queue;
import java.util.stream.Stream;

@Singleton
@ChannelHandler.Sharable
public class ProcessingHandler extends ChannelInboundHandlerAdapter implements BufferingManager.Callback {

    private static final Logger LOGGER = LoggerFactory.getLogger(ProcessingHandler.class);

    private final CacheManager cacheManager;
    private final Storage storage;
    private final NotificationManager notificationManager;
    private final PositionLogger positionLogger;
    private final BufferingManager bufferingManager;
    private final List<BasePositionHandler> positionHandlers;
    private final List<BaseEventHandler> eventHandlers;
    private final PostProcessHandler postProcessHandler;

    private record QueuedPosition(ChannelHandlerContext ctx, Position position) {}

    private final Map<Long, Queue<QueuedPosition>> queues = new HashMap<>();

    private synchronized Queue<QueuedPosition> getQueue(long deviceId) {
        return queues.computeIfAbsent(deviceId, k -> new LinkedList<>());
    }

    @Inject
    public ProcessingHandler(
            Injector injector, Config config,
            CacheManager cacheManager, Storage storage, NotificationManager notificationManager,
            PositionLogger positionLogger) {
        this.cacheManager = cacheManager;
        this.storage = storage;
        this.notificationManager = notificationManager;
        this.positionLogger = positionLogger;
        bufferingManager = new BufferingManager(config, this);

        positionHandlers = Stream.of(
                ReceiptHandler.class,
                ComputedAttributesHandler.Early.class,
                OutdatedHandler.class,
                TimeHandler.class,
                GeolocationHandler.class,
                HemisphereHandler.class,
                MapMatcherHandler.class,
                DistanceHandler.class,
                FilterHandler.class,
                GeofenceHandler.class,
                GeocoderHandler.class,
                SpeedLimitHandler.class,
                MotionHandler.class,
                ComputedAttributesHandler.Late.class,
                DriverHandler.class,
                CopyAttributesHandler.class,
                EngineHoursHandler.class,
                DatabaseHandler.class,
                PositionForwardingHandler.class)
                .map((clazz) -> (BasePositionHandler) injector.getInstance(clazz))
                .filter(Objects::nonNull)
                .toList();

        eventHandlers = Stream.of(
                MediaEventHandler.class,
                CommandResultEventHandler.class,
                OverspeedEventHandler.class,
                BehaviorEventHandler.class,
                FuelEventHandler.class,
                MotionEventHandler.class,
                GeofenceEventHandler.class,
                ProximityEventHandler.class,
                AlarmEventHandler.class,
                IgnitionEventHandler.class,
                MaintenanceEventHandler.class,
                DriverEventHandler.class)
                .map((clazz) -> (BaseEventHandler) injector.getInstance(clazz))
                .filter(Objects::nonNull)
                .toList();

        postProcessHandler = injector.getInstance(PostProcessHandler.class);
    }

    @Override
    public void channelRead(ChannelHandlerContext ctx, Object msg) throws Exception {
        if (msg instanceof Position position) {
            cacheManager.addDevice(position.getDeviceId(), position);
            bufferingManager.accept(ctx, position);
        } else {
            super.channelRead(ctx, msg);
        }
    }

    @Override
    public void onReleased(ChannelHandlerContext context, Position position) {
        Queue<QueuedPosition> queue = getQueue(position.getDeviceId());
        boolean queued;
        synchronized (queue) {
            queued = !queue.isEmpty();
            queue.offer(new QueuedPosition(context, position));
        }
        if (!queued) {
            processPosition(context, position);
        }
    }

    /**
     * Processes one position in one transaction: lock the device (across all servers), reload its stored state,
     * run the handlers, then commit the position, its receipt, the device state and the events together.
     * Deliveries to the outside start after the commit.
     */
    private void processPosition(ChannelHandlerContext ctx, Position position) {
        Transaction transaction;
        try {
            transaction = storage.begin(position.getDeviceId());
        } catch (StorageException e) {
            LOGGER.warn("Failed to lock device {}", position.getDeviceId(), e);
            settle(position, false, false);
            ctx.writeAndFlush(new AcknowledgementHandler.EventHandled(position));
            processNextPosition(position.getDeviceId());
            cacheManager.removeDevice(position.getDeviceId(), position);
            return;
        }
        try (var scope = transaction.bind()) {
            cacheManager.reloadDevice(position.getDeviceId());
        } catch (StorageException e) {
            LOGGER.warn("Failed to load device state {}", position.getDeviceId(), e);
            transaction.setRollbackOnly();
            finishedProcessing(ctx, position, true, transaction);
            return;
        }
        processPositionHandlers(ctx, position, transaction);
    }

    private void processPositionHandlers(ChannelHandlerContext ctx, Position position, Transaction transaction) {
        var iterator = positionHandlers.iterator();
        try (var scope = transaction.bind()) {
            iterator.next().handlePosition(position, new BasePositionHandler.Callback() {
                @Override
                public void processed(boolean filtered) {
                    Runnable continuation = () -> {
                        try (var inner = transaction.bind()) {
                            if (!filtered) {
                                if (iterator.hasNext()) {
                                    iterator.next().handlePosition(position, this);
                                } else {
                                    processEventHandlers(ctx, position, transaction);
                                }
                            } else {
                                finishedProcessing(ctx, position, true, transaction);
                            }
                        }
                    };
                    if (ctx.executor().inEventLoop()) {
                        continuation.run();
                    } else {
                        ctx.executor().execute(continuation);
                    }
                }
            });
        }
    }

    private void processEventHandlers(ChannelHandlerContext ctx, Position position, Transaction transaction) {
        var callback = new BaseEventHandler.Callback() {
            @Override
            public void eventDetected(Event event) {
                notificationManager.updateEvents(Map.of(event, position));
            }

            @Override
            public void eventDetected(Event event, Position eventPosition) {
                notificationManager.updateEvents(Map.of(event, eventPosition));
            }
        };
        eventHandlers.forEach(handler -> handler.analyzePosition(position, callback));
        finishedProcessing(ctx, position, false, transaction);
    }

    private void finishedProcessing(
            ChannelHandlerContext ctx, Position position, boolean filtered, Transaction transaction) {
        if (!filtered) {
            try (var scope = transaction.bind()) {
                postProcessHandler.handlePosition(position, ignore -> { });
            }
        }
        boolean committed = transaction.finish();
        settle(position, filtered, committed);
        if (committed && !filtered) {
            positionLogger.log(ctx, position);
        }
        ctx.writeAndFlush(new AcknowledgementHandler.EventHandled(position));
        processNextPosition(position.getDeviceId());
        cacheManager.removeDevice(position.getDeviceId(), position);
    }

    /** Reports the outcome of a ticketed position, now that its transaction has finished. */
    private static void settle(Position position, boolean filtered, boolean committed) {
        var ticket = position.getIngestTicket();
        if (ticket == null) {
            return;
        }
        IngestOutcome decided = ticket.getDecided();
        if (committed) {
            if (decided != null) {
                ticket.complete(decided);
            } else {
                ticket.complete(filtered ? IngestOutcome.rejected("filtered") : IngestOutcome.stored(position.getId()));
            }
        } else if (decided != null && (decided.status() == IngestOutcome.Status.DUPLICATE
                || decided.status() == IngestOutcome.Status.REJECTED)) {
            ticket.complete(decided); // known without writing anything
        } else {
            ticket.complete(IngestOutcome.failed("storage"));
        }
    }

    private void processNextPosition(long deviceId) {
        Queue<QueuedPosition> queue = getQueue(deviceId);
        QueuedPosition next;
        synchronized (queue) {
            queue.poll(); // remove current position
            next = queue.peek();
        }
        if (next != null) {
            next.ctx().executor().execute(() -> processPosition(next.ctx(), next.position()));
        }
    }

}
