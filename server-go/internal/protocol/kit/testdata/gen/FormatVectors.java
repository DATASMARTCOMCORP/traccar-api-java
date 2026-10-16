import java.io.*;
import java.util.*;
import java.util.zip.GZIPOutputStream;

// Writes "format \t type \t value-bits-or-text \t result" lines for String.format.
public class FormatVectors {
    public static void main(String[] a) throws Exception {
        var out = new PrintWriter(new OutputStreamWriter(new GZIPOutputStream(new FileOutputStream(a[0])), "UTF-8"));
        Random r = new Random(42);
        String[] ff = {"%f", "%.0f", "%.1f", "%.2f", "%.3f", "%.4f", "%.5f", "%.6f", "%.7f", "%08.3f", "%+.2f", "%e", "%.3e", "%.0e", "%10.2f", "%-10.2f|", "%,.2f", "%.10f", "%.15f"};
        double[] special = {0.0, -0.0, 0.5, 1.5, 2.5, 0.125, 0.375, 1.005, 2.675, 1.115, 9.995, 99.995, 0.0005, 0.00049999, 1e-10, 1e20, 123456789.987654321, -1.5, -0.5, 1e-5, 5e-7, Double.MAX_VALUE, Double.MIN_VALUE, 0.1, 0.7, 1.45, 2.345, 1e15, 9.5, 0.95, 0.995};
        List<Double> values = new ArrayList<>();
        for (double d : special) values.add(d);
        for (int i = 0; i < 20000; i++) {
            switch (i % 5) {
                case 0 -> values.add((r.nextDouble() - 0.5) * 400);
                case 1 -> values.add(Math.round(r.nextDouble() * 1e6) / 1e3 + 0.0005);
                case 2 -> values.add(r.nextInt(100000) / 100.0 + 0.005);
                case 3 -> values.add(Double.longBitsToDouble(r.nextLong() & 0x7fffffffffffffffL) % 1e7);
                case 4 -> values.add(Math.pow(10, r.nextDouble() * 30 - 15) * (r.nextBoolean() ? 1 : -1));
            }
        }
        for (double v : values) {
            if (Double.isNaN(v)) continue;
            String f = ff[r.nextInt(ff.length)];
            out.println(f + "\tD\t" + Long.toHexString(Double.doubleToRawLongBits(v)) + "\t" + String.format(f, v));
        }
        String[] fi = {"%d", "%02d", "%04d", "%x", "%X", "%02X", "%04x", "%08X", "%o", "%+d", "%,d", "%5d|", "%-5d|", "%#x", "% d"};
        for (int i = 0; i < 5000; i++) {
            String f = fi[r.nextInt(fi.length)];
            int kind = r.nextInt(4);
            long v = switch (r.nextInt(4)) { case 0 -> r.nextInt(256) - 128; case 1 -> r.nextInt(); case 2 -> r.nextLong(); default -> r.nextInt(100000); };
            switch (kind) {
                case 0 -> out.println(f + "\tI\t" + (int) v + "\t" + String.format(f, (int) v));
                case 1 -> out.println(f + "\tJ\t" + v + "\t" + String.format(f, v));
                case 2 -> out.println(f + "\tB\t" + (byte) v + "\t" + String.format(f, (byte) v));
                default -> out.println(f + "\tS\t" + (short) v + "\t" + String.format(f, (short) v));
            }
        }
        String[] fs = {"%s", "%5s|", "%-5s|", "%.2s", "%S"};
        String[] strs = {"", "a", "abc", "abcdefg", "ç"};
        for (String f : fs) for (String s : strs) out.println(f + "\tT\t" + s + "\t" + String.format(f, s));
        for (String f : fs) {
            out.println(f + "\tD\t" + Long.toHexString(Double.doubleToRawLongBits(1.0)) + "\t" + String.format(f, 1.0));
            out.println(f + "\tI\t42\t" + String.format(f, 42));
        }
        out.close();
    }
}
