import org.traccar.model.Calendar;
import java.io.*;
import java.util.*;
import java.util.stream.*;

// Reads "base64 calendar \t millis \t ..." lines, writes "base64 \t millis \t periods"
// where periods are sorted "start-end" millis joined by ",", or ERR.
public class Periods {
    public static void main(String[] args) throws Exception {
        var in = new BufferedReader(new InputStreamReader(System.in));
        var out = new PrintWriter(new BufferedWriter(new OutputStreamWriter(System.out)));
        Map<String, Calendar> cache = new HashMap<>();
        String line;
        while ((line = in.readLine()) != null) {
            String[] f = line.split("\t");
            String result;
            try {
                Calendar c = cache.get(f[0]);
                if (c == null) {
                    c = new Calendar();
                    c.setData(Base64.getDecoder().decode(f[0]));
                    cache.put(f[0], c);
                }
                result = c.findPeriods(new Date(Long.parseLong(f[1]))).stream()
                        .map(p -> p.getStart().toEpochMilli() + "-" + p.getEnd().toEpochMilli())
                        .sorted().collect(Collectors.joining(","));
            } catch (Exception e) {
                result = "ERR";
            }
            out.println(f[0] + "\t" + f[1] + "\t" + result);
        }
        out.flush();
    }
}
