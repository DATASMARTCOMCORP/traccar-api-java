package org.traccar;

import io.netty.buffer.ByteBuf;
import io.netty.buffer.ByteBufUtil;
import io.netty.buffer.Unpooled;
import io.netty.channel.Channel;
import io.netty.channel.ChannelHandler;
import io.netty.channel.ChannelPipeline;
import io.netty.handler.codec.http.DefaultFullHttpRequest;
import io.netty.handler.codec.http.DefaultFullHttpResponse;
import io.netty.handler.codec.http.DefaultHttpHeaders;
import io.netty.handler.codec.http.HttpHeaders;
import io.netty.handler.codec.http.HttpMethod;
import io.netty.handler.codec.http.HttpResponseStatus;
import io.netty.handler.codec.http.HttpVersion;
import org.traccar.helper.DataConverter;
import org.traccar.model.CellTower;
import org.traccar.model.Command;
import org.traccar.model.Position;
import org.traccar.model.WifiAccessPoint;

import java.nio.charset.StandardCharsets;
import java.text.DateFormat;
import java.text.ParseException;
import java.text.SimpleDateFormat;
import java.util.Collection;
import java.util.Date;
import java.util.List;
import java.util.Map;
import java.util.TimeZone;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

public class ProtocolTest extends BaseTest {

    // Recording for the Go port (not part of Traccar): every decode, frame and command with
    // its input and full output, as JSON lines in the file named by -Doracle.out.
    static final com.fasterxml.jackson.databind.ObjectMapper MAPPER = new com.fasterxml.jackson.databind.ObjectMapper();
    static java.io.PrintWriter OUT;
    static final java.util.List<String> CREATED = new java.util.ArrayList<>();

    static synchronized void emit(java.util.Map<String, Object> record) {
        try {
            if (OUT == null) {
                OUT = new java.io.PrintWriter(new java.io.FileWriter(System.getProperty("oracle.out"), true), true);
            }
            StackWalker.getInstance().walk(frames -> frames
                    .filter(f -> f.getClassName().startsWith("org.traccar.protocol.") && f.getClassName().endsWith("Test"))
                    .findFirst()).ifPresent(f -> {
                        record.put("test", f.getClassName().substring("org.traccar.protocol.".length()));
                        record.put("method", f.getMethodName());
                        record.put("line", f.getLineNumber());
                    });
            record.put("now", System.currentTimeMillis());
            OUT.println(MAPPER.writeValueAsString(record));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    static Object describeInput(Object o) {
        var m = new java.util.LinkedHashMap<String, Object>();
        if (o instanceof ByteBuf b) {
            m.put("type", "bytes");
            m.put("hex", ByteBufUtil.hexDump(b));
        } else if (o instanceof String str) {
            m.put("type", "string");
            m.put("text", str);
        } else if (o instanceof io.netty.handler.codec.http.FullHttpRequest r) {
            m.put("type", "http");
            m.put("method", r.method().name());
            m.put("uri", r.uri());
            var h = new java.util.ArrayList<String[]>();
            r.headers().forEach(e -> h.add(new String[]{e.getKey(), e.getValue()}));
            m.put("headers", h);
            m.put("hex", ByteBufUtil.hexDump(r.content()));
        } else if (o instanceof io.netty.handler.codec.http.FullHttpResponse r) {
            m.put("type", "httpResponse");
            m.put("status", r.status().code());
            m.put("hex", ByteBufUtil.hexDump(r.content()));
        } else if (o == null) {
            m.put("type", "null");
        } else {
            m.put("type", o.getClass().getName());
            m.put("text", String.valueOf(o));
        }
        return m;
    }

    static Object describePosition(Position p) {
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("deviceId", p.getDeviceId());
        m.put("protocol", p.getProtocol());
        m.put("deviceTime", p.getDeviceTime() == null ? null : p.getDeviceTime().getTime());
        m.put("fixTime", p.getFixTime() == null ? null : p.getFixTime().getTime());
        m.put("outdated", p.getOutdated());
        m.put("valid", p.getValid());
        m.put("latitude", Double.doubleToRawLongBits(p.getLatitude()));
        m.put("longitude", Double.doubleToRawLongBits(p.getLongitude()));
        m.put("altitude", Double.doubleToRawLongBits(p.getAltitude()));
        m.put("speed", Double.doubleToRawLongBits(p.getSpeed()));
        m.put("course", Double.doubleToRawLongBits(p.getCourse()));
        m.put("accuracy", Double.doubleToRawLongBits(p.getAccuracy()));
        m.put("address", p.getAddress());
        try {
            m.put("attributes", MAPPER.writeValueAsString(p.getAttributes()));
            m.put("network", p.getNetwork() == null ? null : MAPPER.writeValueAsString(p.getNetwork()));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
        var types = new java.util.LinkedHashMap<String, String>();
        p.getAttributes().forEach((k, v) -> types.put(k, v == null ? "null" : v.getClass().getSimpleName()));
        m.put("types", types);
        m.put("geofenceIds", p.getGeofenceIds());
        return m;
    }

    static Object describeOutput(Object o) {
        if (o instanceof Position p) {
            return java.util.Map.of("position", describePosition(p));
        } else if (o instanceof Collection<?> c) {
            var list = new java.util.ArrayList<Object>();
            for (Object e : c) list.add(e instanceof Position p ? describePosition(p) : String.valueOf(e));
            return java.util.Map.of("positions", list);
        } else if (o instanceof ByteBuf b) {
            return java.util.Map.of("bytes", ByteBufUtil.hexDump(b));
        } else if (o instanceof String str) {
            return java.util.Map.of("text", str);
        } else if (o == null) {
            return null;
        }
        return java.util.Map.of("other", o.getClass().getName() + ": " + o);
    }

    protected Object recordDecode(BaseProtocolDecoder decoder, Object object) throws Exception {
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("kind", "decode");
        m.put("decoder", decoder.getClass().getSimpleName());
        m.put("instance", System.identityHashCode(decoder));
        m.put("input", describeInput(object));
        m.put("before", System.currentTimeMillis());
        try {
            Object result = decoder.decode(null, null, object);
            m.put("output", describeOutput(result));
            emit(m);
            return result;
        } catch (Exception | Error e) {
            m.put("error", e.getClass().getName() + ": " + e.getMessage());
            emit(m);
            throw e;
        }
    }


    protected Position position(String time, boolean valid, double lat, double lon) throws ParseException {

        Position position = new Position();

        DateFormat dateFormat = new SimpleDateFormat("yyyy-MM-dd HH:mm:ss.SSS");
        dateFormat.setTimeZone(TimeZone.getTimeZone("UTC"));
        position.setTime(dateFormat.parse(time));
        position.setValid(valid);
        position.setLatitude(lat);
        position.setLongitude(lon);

        return position;
    }

    private String concatenateStrings(String... strings) {
        StringBuilder builder = new StringBuilder();
        for (String s : strings) {
            builder.append(s);
        }
        return builder.toString();
    }

    protected ByteBuf concatenateBuffers(ByteBuf... buffers) {
        ByteBuf result = Unpooled.buffer();
        for (ByteBuf buf : buffers) {
            result.writeBytes(buf);
        }
        CREATED.add(ByteBufUtil.hexDump(result));
        return result;
    }

    protected ByteBuf binary(String... data) {
        ByteBuf b = Unpooled.wrappedBuffer(DataConverter.parseHex(concatenateStrings(data)));
        CREATED.add(ByteBufUtil.hexDump(b));
        return b;
    }

    protected String text(String... data) {
        return concatenateStrings(data);
    }

    protected ByteBuf buffer(String... data) {
        ByteBuf b = Unpooled.copiedBuffer(concatenateStrings(data), StandardCharsets.ISO_8859_1);
        CREATED.add(ByteBufUtil.hexDump(b));
        return b;
    }

    protected DefaultFullHttpRequest request(String url) {
        return request(HttpMethod.GET, url);
    }

    protected DefaultFullHttpRequest request(HttpMethod method, String url) {
        return new DefaultFullHttpRequest(HttpVersion.HTTP_1_1, method, url);
    }

    protected DefaultFullHttpRequest request(HttpMethod method, String url, ByteBuf data) {
        return new DefaultFullHttpRequest(HttpVersion.HTTP_1_1, method, url, data);
    }

    protected DefaultFullHttpRequest request(HttpMethod method, String url, HttpHeaders headers) {
        return new DefaultFullHttpRequest(HttpVersion.HTTP_1_1, method, url, Unpooled.buffer(), headers, new DefaultHttpHeaders());
    }

    protected DefaultFullHttpRequest request(HttpMethod method, String url, HttpHeaders headers, ByteBuf data) {
        return new DefaultFullHttpRequest(HttpVersion.HTTP_1_1, method, url, data, headers, new DefaultHttpHeaders());
    }

    protected DefaultFullHttpResponse response(ByteBuf data) {
        return new DefaultFullHttpResponse(HttpVersion.HTTP_1_1, HttpResponseStatus.OK, data);
    }

    protected void verifyNotNull(BaseProtocolDecoder decoder, Object object) throws Exception {
        assertNotNull(recordDecode(decoder, object));
    }

    protected void verifyNull(Object object) {
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("kind", "frameNull");
        m.put("created", new java.util.ArrayList<>(CREATED));
        m.put("output", describeOutput(object));
        emit(m);
        CREATED.clear();
        assertNull(object);
    }

    protected void verifyNull(BaseProtocolDecoder decoder, Object object) throws Exception {
        assertNull(recordDecode(decoder, object));
    }

    protected void verifyAttribute(BaseProtocolDecoder decoder, Object object, String key, Object expected) throws Exception {
        Object decodedObject = recordDecode(decoder, object);
        Position position;
        if (decodedObject instanceof Collection) {
            position = (Position) ((Collection<?>) decodedObject).iterator().next();
        } else {
            position = (Position) decodedObject;
        }
        switch (key) {
            case "speed" -> assertEquals(expected, position.getSpeed());
            case "course" -> assertEquals(expected, position.getCourse());
            case "altitude" -> assertEquals(expected, position.getAltitude());
            case "network" -> assertEquals(expected, position.getNetwork());

            default -> assertEquals(expected, position.getAttributes().get(key));
        }
    }

    protected void verifyAttributes(BaseProtocolDecoder decoder, Object object) throws Exception {
        verifyDecodedPosition(recordDecode(decoder, object), false, true, null);
    }

    protected void verifyPosition(BaseProtocolDecoder decoder, Object object) throws Exception {
        verifyDecodedPosition(recordDecode(decoder, object), true, false, null);
    }

    protected void verifyPosition(BaseProtocolDecoder decoder, Object object, Position position) throws Exception {
        verifyDecodedPosition(recordDecode(decoder, object), true, false, position);
    }

    protected void verifyPositions(BaseProtocolDecoder decoder, Object object) throws Exception {
        verifyDecodedList(recordDecode(decoder, object), true, null);
    }

    protected void verifyPositions(BaseProtocolDecoder decoder, boolean checkLocation, Object object) throws Exception {
        verifyDecodedList(recordDecode(decoder, object), checkLocation, null);
    }

    protected void verifyPositions(BaseProtocolDecoder decoder, Object object, Position position) throws Exception {
        verifyDecodedList(recordDecode(decoder, object), true, position);
    }

    private void verifyDecodedList(Object decodedObject, boolean checkLocation, Position expected) {

        assertNotNull(decodedObject, "list is null");
        assertInstanceOf(List.class, decodedObject, "not a list");
        assertFalse(((List<?>) decodedObject).isEmpty(), "list is empty");

        for (Object item : (List<?>) decodedObject) {
            verifyDecodedPosition(item, checkLocation, false, expected);
        }

    }

    private void verifyDecodedPosition(Object decodedObject, boolean checkLocation, boolean checkAttributes, Position expected) {

        assertNotNull(decodedObject, "position is null");
        assertInstanceOf(Position.class, decodedObject, "not a position");

        Position position = (Position) decodedObject;

        if (checkLocation) {

            if (expected != null) {

                if (expected.getFixTime() != null) {
                    DateFormat dateFormat = new SimpleDateFormat("yyyy-MM-dd HH:mm:ss.SSS");
                    dateFormat.setTimeZone(TimeZone.getTimeZone("UTC"));
                    assertEquals(dateFormat.format(expected.getFixTime()), dateFormat.format(position.getFixTime()), "time");
                }
                assertEquals(expected.getValid(), position.getValid(), "valid");
                assertEquals(expected.getLatitude(), position.getLatitude(), 0.00001, "latitude");
                assertEquals(expected.getLongitude(), position.getLongitude(), 0.00001, "longitude");

            } else {

                assertNotNull(position.getServerTime());
                assertNotNull(position.getFixTime());
                assertTrue(position.getFixTime().after(new Date(915148800000L)), "year > 1999");
                assertTrue(position.getFixTime().getTime() < System.currentTimeMillis() + 25 * 3600000, "time < +25 h");

                assertTrue(position.getLatitude() >= -90, "latitude >= -90");
                assertTrue(position.getLatitude() <= 90, "latitude <= 90");

                assertTrue(position.getLongitude() >= -180, "longitude >= -180");
                assertTrue(position.getLongitude() <= 180, "longitude <= 180");

            }

            assertTrue(position.getAltitude() >= -12262, "altitude >= -12262");
            assertTrue(position.getAltitude() <= 18000, "altitude <= 18000");

            assertTrue(position.getSpeed() >= 0, "speed >= 0");
            assertTrue(position.getSpeed() <= 869, "speed <= 869");

            assertTrue(position.getCourse() >= 0, "course >= 0");
            assertTrue(position.getCourse() <= 360, "course <= 360");

            assertNotNull(position.getProtocol(), "protocol is null");

            assertTrue(position.getDeviceId() > 0, "deviceId > 0");

        }

        Map<String, Object> attributes = position.getAttributes();

        if (checkAttributes) {
            assertFalse(attributes.isEmpty(), "no attributes");
        }

        if (attributes.containsKey(Position.KEY_INDEX)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_INDEX));
        }

        if (attributes.containsKey(Position.KEY_HDOP)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_HDOP));
        }

        if (attributes.containsKey(Position.KEY_VDOP)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_VDOP));
        }

        if (attributes.containsKey(Position.KEY_PDOP)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_PDOP));
        }

        if (attributes.containsKey(Position.KEY_SATELLITES)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_SATELLITES));
        }

        if (attributes.containsKey(Position.KEY_SATELLITES_VISIBLE)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_SATELLITES_VISIBLE));
        }

        if (attributes.containsKey(Position.KEY_RSSI)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_RSSI));
        }

        if (attributes.containsKey(Position.KEY_ODOMETER)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_ODOMETER));
        }

        if (attributes.containsKey(Position.KEY_RPM)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_RPM));
        }

        if (attributes.containsKey(Position.KEY_FUEL)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_FUEL));
        }

        if (attributes.containsKey(Position.KEY_FUEL_USED)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_FUEL_USED));
        }

        if (attributes.containsKey(Position.KEY_POWER)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_POWER));
        }

        if (attributes.containsKey(Position.KEY_BATTERY)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_BATTERY));
        }

        if (attributes.containsKey(Position.KEY_BATTERY_LEVEL)) {
            int batteryLevel = ((Number) attributes.get(Position.KEY_BATTERY_LEVEL)).intValue();
            assertTrue(batteryLevel <= 100 && batteryLevel >= 0);
        }

        if (attributes.containsKey(Position.KEY_CHARGE)) {
            assertInstanceOf(Boolean.class, attributes.get(Position.KEY_CHARGE));
        }

        if (attributes.containsKey(Position.KEY_IGNITION)) {
            assertInstanceOf(Boolean.class, attributes.get(Position.KEY_IGNITION));
        }

        if (attributes.containsKey(Position.KEY_MOTION)) {
            assertInstanceOf(Boolean.class, attributes.get(Position.KEY_MOTION));
        }

        if (attributes.containsKey(Position.KEY_ARCHIVE)) {
            assertInstanceOf(Boolean.class, attributes.get(Position.KEY_ARCHIVE));
        }

        if (attributes.containsKey(Position.KEY_DRIVER_UNIQUE_ID)) {
            assertInstanceOf(String.class, attributes.get(Position.KEY_DRIVER_UNIQUE_ID));
        }

        if (attributes.containsKey(Position.KEY_STEPS)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_STEPS));
        }

        if (attributes.containsKey(Position.KEY_ROAMING)) {
            assertInstanceOf(Boolean.class, attributes.get(Position.KEY_ROAMING));
        }

        if (attributes.containsKey(Position.KEY_HOURS)) {
            assertInstanceOf(Number.class, attributes.get(Position.KEY_HOURS));
        }

        if (attributes.containsKey(Position.KEY_RESULT)) {
            assertInstanceOf(String.class, attributes.get(Position.KEY_RESULT));
        }

        if (position.getNetwork() != null) {
            if (position.getNetwork().getCellTowers() != null) {
                for (CellTower cellTower : position.getNetwork().getCellTowers()) {
                    checkInteger(cellTower.getMobileCountryCode(), 0, 999);
                    checkInteger(cellTower.getMobileNetworkCode(), 0, 999);
                    checkInteger(cellTower.getLocationAreaCode(), 1, 65535);
                    checkInteger(cellTower.getCellId(), 0, 268435455);
                }
            }

            if (position.getNetwork().getWifiAccessPoints() != null) {
                for (WifiAccessPoint wifiAccessPoint : position.getNetwork().getWifiAccessPoints()) {
                    assertTrue(wifiAccessPoint.getMacAddress().matches("((\\p{XDigit}{2}):){5}(\\p{XDigit}{2})"));
                }
            }
        }

    }

    private void checkInteger(Object value, int min, int max) {
        assertNotNull(value, "value is null");
        assertTrue(value instanceof Integer || value instanceof Long, "not int or long");
        long number = ((Number) value).longValue();
        assertTrue(number >= min, "value too low");
        assertTrue(number <= max, "value too high");
    }

    protected void verifyCommand(
            BaseProtocolEncoder encoder, Command command, ByteBuf expected) {
        Object actual = encoder.encodeCommand(command);
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("kind", "command");
        m.put("encoder", encoder.getClass().getSimpleName());
        try {
            m.put("command", MAPPER.writeValueAsString(command));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
        m.put("expected", ByteBufUtil.hexDump(expected));
        m.put("output", describeOutput(actual));
        emit(m);
        CREATED.clear();
        verifyFrame(expected, actual);
    }

    protected Object encodeCommand(
            BaseProtocolEncoder encoder, BaseProtocolDecoder decoder, Command command) {
        var pipeline = mock(ChannelPipeline.class);
        when(pipeline.iterator()).thenReturn(
                List.<Map.Entry<String, ChannelHandler>>of(Map.entry("decoder", decoder)).iterator());
        var channel = mock(Channel.class);
        when(channel.pipeline()).thenReturn(pipeline);
        Object result = encoder.encodeCommand(channel, command);
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("kind", "encode");
        m.put("encoder", encoder.getClass().getSimpleName());
        m.put("decoder", decoder.getClass().getSimpleName());
        m.put("instance", System.identityHashCode(decoder));
        try {
            m.put("command", MAPPER.writeValueAsString(command));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
        m.put("output", describeOutput(result));
        emit(m);
        return result;
    }

    // The encoder tests that call encoder.encodeCommand directly are recorded through
    // rewritten copies that wrap the call in recordEncode.
    protected <T> T recordEncode(Object encoder, Command command, T result, Object... extra) {
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("kind", "encode");
        m.put("encoder", encoder.getClass().getSimpleName());
        try {
            m.put("command", MAPPER.writeValueAsString(command));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
        var args = new java.util.ArrayList<String>();
        for (Object x : extra) {
            args.add(x instanceof java.util.Date d ? String.valueOf(d.getTime()) : String.valueOf(x));
        }
        m.put("args", args);
        m.put("output", describeOutput(result));
        emit(m);
        return result;
    }

    protected void verifyFrame(ByteBuf expected, Object object) {
        var m = new java.util.LinkedHashMap<String, Object>();
        m.put("kind", "frame");
        m.put("created", new java.util.ArrayList<>(CREATED));
        m.put("expected", ByteBufUtil.hexDump(expected));
        m.put("output", describeOutput(object));
        emit(m);
        CREATED.clear();
        assertNotNull(object, "buffer is null");
        assertInstanceOf(ByteBuf.class, object, "not a buffer");
        assertEquals(ByteBufUtil.hexDump(expected), ByteBufUtil.hexDump((ByteBuf) object));
    }

}
