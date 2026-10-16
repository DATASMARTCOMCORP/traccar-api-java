// Records what Traccar's notification templates render to, for the Go engine's tests:
// java -cp <tracker-server.jar>:. Record <templates root> <prod fixtures root> <cases.json> > renders.jsonl
// It builds the Velocity context as NotificationFormatter and TextTemplateFormatter do, with
// a fixed token and web URL, and renders every template of every language for every case.
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.apache.velocity.Template;
import org.apache.velocity.VelocityContext;
import org.apache.velocity.app.VelocityEngine;
import org.apache.velocity.tools.generic.DateTool;
import org.apache.velocity.tools.generic.EscapeTool;
import org.apache.velocity.tools.generic.NumberTool;
import org.traccar.helper.model.UserUtil;
import org.traccar.model.Device;
import org.traccar.model.Driver;
import org.traccar.model.Event;
import org.traccar.model.Geofence;
import org.traccar.model.Maintenance;
import org.traccar.model.Position;
import org.traccar.model.Server;
import org.traccar.model.User;

import java.io.File;
import java.io.StringWriter;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

public class Record {

    public static void main(String[] args) throws Exception {
        ObjectMapper mapper = new ObjectMapper();
        mapper.configure(com.fasterxml.jackson.databind.DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
        JsonNode cases = mapper.readTree(new File(args[2]));

        java.util.Properties properties = new java.util.Properties();
        properties.setProperty("resource.loader.file.path", "/");
        properties.setProperty("web.url", "http://sir.example:8082");
        VelocityEngine engine = new VelocityEngine();
        engine.init(properties);

        String[][] sets = {{"repo", args[0]}, {"prod", args[1]}};
        for (JsonNode c : cases) {
            for (String[] set : sets) {
                File[] languages = new File(set[1], "notifications").listFiles();
                if (languages == null) {
                    languages = new File(set[1]).listFiles();
                }
                Arrays.sort(languages);
                for (File language : languages) {
                    File[] templates = language.listFiles((d, n) -> n.endsWith(".vm"));
                    Arrays.sort(templates);
                    for (File file : templates) {
                        ObjectNode out = mapper.createObjectNode();
                        out.put("case", c.get("name").asText());
                        out.put("set", set[0]);
                        out.put("lang", language.getName());
                        out.put("template", file.getName().replace(".vm", ""));
                        try {
                            VelocityContext context = context(mapper, c);
                            StringWriter writer = new StringWriter();
                            Template template = engine.getTemplate(file.getAbsolutePath(), StandardCharsets.UTF_8.name());
                            template.merge(context, writer);
                            putString(out, "subject", context.get("subject"));
                            putString(out, "digest", context.get("digest"));
                            out.put("body", writer.toString());
                        } catch (Exception e) {
                            out.put("error", e.getClass().getSimpleName());
                        }
                        System.out.println(mapper.writeValueAsString(out));
                    }
                }
            }
        }
    }

    private static void putString(ObjectNode out, String key, Object value) {
        if (value == null) {
            out.putNull(key);
        } else {
            out.put(key, value.toString());
        }
    }

    private static <T> T read(ObjectMapper mapper, JsonNode c, String key, Class<T> clazz) throws Exception {
        JsonNode node = c.get(key);
        return node == null || node.isNull() ? null : mapper.treeToValue(node, clazz);
    }

    // NotificationFormatter.formatMessage and TextTemplateFormatter.prepareContext.
    private static VelocityContext context(ObjectMapper mapper, JsonNode c) throws Exception {
        Server server = read(mapper, c, "server", Server.class);
        User user = read(mapper, c, "user", User.class);
        Device device = read(mapper, c, "device", Device.class);
        Event event = read(mapper, c, "event", Event.class);
        Position position = read(mapper, c, "position", Position.class);

        VelocityContext context = new VelocityContext();
        context.put("user", user);
        context.put("timezone", UserUtil.getTimezone(server, user));
        context.put("token", "TOKEN123");
        context.put("webUrl", "http://sir.example:8082");
        context.put("dateTool", new DateTool());
        context.put("esc", new EscapeTool());
        context.put("numberTool", new NumberTool());
        context.put("locale", Locale.getDefault());
        context.put("language", UserUtil.getLanguage(server, user));

        context.put("notification", null);
        context.put("device", device);
        context.put("event", event);
        Map<String, String> translations = new HashMap<>();
        if (c.has("translations")) {
            c.get("translations").fields().forEachRemaining(e -> translations.put(e.getKey(), e.getValue().asText()));
        }
        context.put("translations", translations);
        if (position != null) {
            context.put("position", position);
            context.put("speedUnit", UserUtil.getSpeedUnit(server, user));
            context.put("distanceUnit", UserUtil.getDistanceUnit(server, user));
            context.put("volumeUnit", UserUtil.getVolumeUnit(server, user));
        }
        if (event.getGeofenceId() != 0) {
            context.put("geofence", read(mapper, c, "geofence", Geofence.class));
        }
        if (event.getMaintenanceId() != 0) {
            context.put("maintenance", read(mapper, c, "maintenance", Maintenance.class));
        }
        String driverUniqueId = event.getString(Position.KEY_DRIVER_UNIQUE_ID);
        if (driverUniqueId != null) {
            Driver driver = read(mapper, c, "driver", Driver.class);
            context.put("driver", driver != null && driver.getUniqueId().equals(driverUniqueId) ? driver : null);
        }
        return context;
    }
}
