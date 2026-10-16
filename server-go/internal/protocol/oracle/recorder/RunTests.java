import org.junit.platform.launcher.*;
import org.junit.platform.launcher.core.*;
import org.junit.platform.launcher.listeners.*;
import static org.junit.platform.engine.discovery.DiscoverySelectors.selectPackage;
import static org.junit.platform.engine.discovery.DiscoverySelectors.selectClass;

public class RunTests {
    public static void main(String[] a) throws Exception {
        var builder = LauncherDiscoveryRequestBuilder.request();
        if (a.length == 0) builder.selectors(selectPackage("org.traccar.protocol"));
        else for (String c : a) builder.selectors(selectClass("org.traccar.protocol." + c));
        var listener = new SummaryGeneratingListener();
        LauncherFactory.create().execute(builder.build(), listener);
        listener.getSummary().printTo(new java.io.PrintWriter(System.out));
        listener.getSummary().printFailuresTo(new java.io.PrintWriter(System.out), 3);
    }
}
