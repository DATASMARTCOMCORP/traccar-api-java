package org.traccar.sqs;

import io.netty.bootstrap.ServerBootstrap;
import io.netty.channel.ChannelInitializer;
import io.netty.channel.MultiThreadIoEventLoopGroup;
import io.netty.channel.nio.NioIoHandler;
import io.netty.channel.socket.SocketChannel;
import io.netty.channel.socket.nio.NioServerSocketChannel;
import io.netty.handler.codec.http.HttpObjectAggregator;
import io.netty.handler.codec.http.HttpServerCodec;
import io.netty.handler.timeout.ReadTimeoutHandler;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.traccar.config.Config;
import org.traccar.config.Keys;

import java.net.InetSocketAddress;

/**
 * Runs the SQS publisher as its own process, next to the server but independent of it: it starts without the
 * database, and a restart, crash or pause of the server does not stop it from accepting reports.
 *
 * <p>Usage: {@code java -cp tracker-server.jar org.traccar.sqs.SqsPublisherMain <config file>}
 */
public final class SqsPublisherMain {

    private static final Logger LOGGER = LoggerFactory.getLogger(SqsPublisherMain.class);

    /** A full batch of 500 positions is far below this, and below the SQS message limit. */
    private static final int MAX_BODY = 256 * 1024;
    private static final int READ_TIMEOUT_SECONDS = 60;

    private SqsPublisherMain() {
    }

    public static void main(String[] args) throws Exception {
        if (args.length == 0) {
            throw new IllegalArgumentException("Usage: SqsPublisherMain <config file>");
        }
        Config config = new Config(args[args.length - 1]);
        if (!config.hasKey(Keys.SQS_QUEUE_URL)) {
            throw new IllegalArgumentException("Missing " + Keys.SQS_QUEUE_URL.getKey());
        }
        var sqs = SqsClients.create(config);
        var handler = new SqsPositionPublisher(sqs, config.getString(Keys.SQS_QUEUE_URL));

        var ioHandlerFactory = NioIoHandler.newFactory();
        var bossGroup = new MultiThreadIoEventLoopGroup(1, ioHandlerFactory);
        var workerGroup = new MultiThreadIoEventLoopGroup(ioHandlerFactory);
        var bootstrap = new ServerBootstrap()
                .group(bossGroup, workerGroup)
                .channel(NioServerSocketChannel.class)
                .childHandler(new ChannelInitializer<SocketChannel>() {
                    @Override
                    protected void initChannel(SocketChannel channel) {
                        channel.pipeline()
                                .addLast(new ReadTimeoutHandler(READ_TIMEOUT_SECONDS))
                                .addLast(new HttpServerCodec())
                                .addLast(new HttpObjectAggregator(MAX_BODY))
                                .addLast(handler);
                    }
                });

        int port = config.getInteger(Keys.SQS_PUBLISHER_PORT);
        String address = config.getString(Keys.SQS_PUBLISHER_ADDRESS);
        var channel = bootstrap.bind(address != null
                ? new InetSocketAddress(address, port) : new InetSocketAddress(port)).sync().channel();
        LOGGER.info("SQS publisher listening on {}, publishing to {}",
                channel.localAddress(), config.getString(Keys.SQS_QUEUE_URL));

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            LOGGER.info("Stopping SQS publisher...");
            channel.close().syncUninterruptibly();
            bossGroup.shutdownGracefully().syncUninterruptibly();
            workerGroup.shutdownGracefully().syncUninterruptibly();
            sqs.close();
        }));
        channel.closeFuture().sync();
    }

}
