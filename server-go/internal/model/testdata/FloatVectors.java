import java.io.*;
import java.util.*;
import java.util.zip.GZIPOutputStream;
public class FloatVectors {
    public static void main(String[] a) throws Exception {
        var out = new PrintWriter(new OutputStreamWriter(new GZIPOutputStream(new FileOutputStream(a[0])), "UTF-8"));
        Random r = new Random(3);
        float[] special = {0f, -0f, Float.MIN_VALUE, Float.MAX_VALUE, Float.MIN_NORMAL, 1e-3f, 1e7f, 9999999f, 0.001f, 0.1f, 1.1f, 2e-45f, 3.4e38f, 1e10f, 123.456f};
        for (float f : special) out.println(Integer.toHexString(Float.floatToRawIntBits(f)) + "\t" + Float.toString(f));
        for (int i = 0; i < 50000; i++) {
            float f = switch (i % 3) { case 0 -> Float.intBitsToFloat(r.nextInt()); case 1 -> (r.nextFloat() - 0.5f) * 1000; default -> (float) Math.pow(10, r.nextDouble() * 20 - 10); };
            if (Float.isNaN(f) || Float.isInfinite(f)) continue;
            out.println(Integer.toHexString(Float.floatToRawIntBits(f)) + "\t" + Float.toString(f));
        }
        out.close();
    }
}
