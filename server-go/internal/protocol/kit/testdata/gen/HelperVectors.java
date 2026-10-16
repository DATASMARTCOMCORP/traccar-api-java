import io.netty.buffer.*;
import java.io.*;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.zip.GZIPOutputStream;
import org.traccar.helper.*;

// Writes "kind \t input \t output" lines for Traccar helpers.
public class HelperVectors {
    public static void main(String[] a) throws Exception {
        var out = new PrintWriter(new OutputStreamWriter(new GZIPOutputStream(new FileOutputStream(a[0])), "UTF-8"));
        Random r = new Random(7);
        Object[][] algos = {
            {"CRC8_EGTS", Checksum.CRC8_EGTS}, {"CRC8_ROHC", Checksum.CRC8_ROHC}, {"CRC8_DALLAS", Checksum.CRC8_DALLAS},
            {"CRC16_IBM", Checksum.CRC16_IBM}, {"CRC16_X25", Checksum.CRC16_X25}, {"CRC16_MODBUS", Checksum.CRC16_MODBUS},
            {"CRC16_CCITT_FALSE", Checksum.CRC16_CCITT_FALSE}, {"CRC16_KERMIT", Checksum.CRC16_KERMIT},
            {"CRC16_XMODEM", Checksum.CRC16_XMODEM}, {"CRC32_STANDARD", Checksum.CRC32_STANDARD},
            {"CRC32_MPEG2", Checksum.CRC32_MPEG2}};
        for (int i = 0; i < 400; i++) {
            byte[] b = new byte[r.nextInt(64)];
            r.nextBytes(b);
            String hex = HexFormat.of().formatHex(b);
            for (Object[] al : algos) {
                String name = (String) al[0];
                var alg = (Checksum.Algorithm) al[1];
                int v = name.startsWith("CRC8") ? Checksum.crc8(alg, ByteBuffer.wrap(b))
                        : name.startsWith("CRC16") ? Checksum.crc16(alg, ByteBuffer.wrap(b))
                        : Checksum.crc32(alg, ByteBuffer.wrap(b));
                out.println(name + "\t" + hex + "\t" + v);
            }
            out.println("xor\t" + hex + "\t" + Checksum.xor(ByteBuffer.wrap(b)));
            out.println("sum\t" + hex + "\t" + Checksum.sum(ByteBuffer.wrap(b)));
            out.println("modulo256\t" + hex + "\t" + Checksum.modulo256(ByteBuffer.wrap(b)));
            if (b.length % 2 == 0) out.println("ip\t" + hex + "\t" + Checksum.ip(ByteBuffer.wrap(b)));
            StringBuilder s = new StringBuilder();
            for (int j = 0; j < b.length; j++) s.append((char) (32 + (b[j] & 0x5f)));
            String str = s.toString();
            out.println("xorString\t" + HexFormat.of().formatHex(str.getBytes("UTF-8")) + "\t" + Checksum.xor(str));
            out.println("nmea\t" + HexFormat.of().formatHex(str.getBytes("UTF-8")) + "\t" + Checksum.nmea(str));
            out.println("sumString\t" + HexFormat.of().formatHex(str.getBytes("UTF-8")) + "\t" + Checksum.sum(str));
            long imei = Math.abs(r.nextLong() % 100000000000000L);
            out.println("luhn\t" + imei + "\t" + Checksum.luhn(imei));
            // base64: random text from a mixed alphabet
            String alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/-_= \n*";
            StringBuilder b64 = new StringBuilder();
            int n = r.nextInt(40);
            for (int j = 0; j < n; j++) b64.append(alphabet.charAt(r.nextInt(alphabet.length())));
            if (i % 3 == 0) b64 = new StringBuilder(Base64.getEncoder().encodeToString(b));
            out.println("base64\t" + HexFormat.of().formatHex(b64.toString().getBytes("UTF-8")) + "\t" + HexFormat.of().formatHex(DataConverter.parseBase64(b64.toString())));
            // BitBuffer reads
            var bb = new BitBuffer(Unpooled.wrappedBuffer(b));
            StringBuilder bits = new StringBuilder();
            try {
                int left = b.length * 8;
                while (left > 0) {
                    int len = 1 + r.nextInt(Math.min(left, 31));
                    boolean signed = r.nextBoolean();
                    bits.append(signed ? "s" : "u").append(len).append("=").append(signed ? bb.readSigned(len) : bb.readUnsigned(len)).append(" ");
                    left -= len;
                }
            } catch (Exception e) { bits.append("error"); }
            out.println("bits\t" + hex + "\t" + bits.toString().trim());
            int code = r.nextInt(65536);
            out.println("obdCode\t" + code + "\t" + ObdDecoder.decodeCode(code));
            if (b.length >= 4) {
                var bcd = Unpooled.wrappedBuffer(b);
                out.println("bcdCoord\t" + hex.substring(0, 8) + "\t" + Long.toHexString(Double.doubleToRawLongBits(BcdUtil.readCoordinate(bcd))));
                int digits = 1 + r.nextInt(Math.min(b.length * 2 - 1, 9));
                out.println("bcd" + digits + "\t" + hex + "\t" + BcdUtil.readInteger(Unpooled.wrappedBuffer(b), digits));
            }
        }
        out.close();
    }
}
