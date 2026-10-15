import java.util.*;

// Golden iteration orders of java.util.HashSet<String>: "new" = items added one by one to
// new HashSet<>(); "copy" = new HashSet<>(that set) followed by add("custom"), which is
// what BaseProtocol.getSupportedDataCommands returns. Output: kind<TAB>input|...<TAB>order|...
public class HashOrder {
    public static void main(String[] args) {
        List<List<String>> inputs = new ArrayList<>(List.of(
            List.of("outputControl", "rebootDevice", "positionSingle", "engineStop", "engineResume", "alarmArm", "alarmDisarm"),
            List.of("web", "mail", "sms", "command", "firebase"),
            List.of("osmand"), List.of("osmand", "suntech", "gt06", "teltonika", "h02", "meitrack", "çã", "😀"),
            List.of()));
        Random r = new Random(15);
        for (int n = 1; n <= 40; n++) {
            List<String> l = new ArrayList<>();
            for (int i = 0; i < n; i++) l.add(Long.toString(r.nextLong() & 0xffffffffL, 36) + (char) ('a' + r.nextInt(26)));
            inputs.add(l);
        }
        for (List<String> in : inputs) {
            Set<String> s = new HashSet<>();
            s.addAll(in);
            System.out.println("new\t" + String.join("|", in) + "\t" + String.join("|", s));
            Set<String> c = new HashSet<>(s);
            c.add("custom");
            System.out.println("copy\t" + String.join("|", in) + "\t" + String.join("|", c));
        }
    }
}
