package observerfixture;

import com.flanksource.telltale.observer.*;
import java.util.*;
import java.lang.management.ManagementFactory;
import javax.management.ObjectName;

/** Loaded only after the packaged agent has been attached to the existing JVM. */
public final class NoRestartOtelCapture implements ObserverAdapter {
  private ObserverSubscription operations;
  private ObserverSubscription spans;
  private final List<String> profiles = new ArrayList<>();
  public String id() { return "gate"; }
  public Set<String> kinds() { return Collections.singleton("gate"); }
  public boolean matchesType(String name, Set<String> hierarchy) { return false; }
  public boolean matchesMethod(String name, String descriptor) { return false; }
  public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return null; }
  public Object onExit(Object token, Object result, Throwable error) { return result; }
  public void onOperation(Map<String, Object> metadata) {
    if (!"gate-http".equals(metadata.get("instrumentationName")) && !"http".equals(metadata.get("signal"))) return;
    Map<String, Object> row = new LinkedHashMap<>(metadata);
    row.remove("request"); row.remove("context");
    ObserverRuntime.emit("gate", row);
  }
  public static NoRestartOtelCapture arm() throws Exception {
    NoRestartOtelCapture result = new NoRestartOtelCapture();
    ObserverRuntime.register(result);
    result.operations = ObserverRuntime.subscribe("gate", "gate-operations", Collections.emptyMap());
    result.spans = ObserverRuntime.subscribe("spans", "gate-spans", Collections.emptyMap());
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("direction", "outgoing"); options.put("bodies", "64KiB");
    options.put("paths", Collections.singletonList("/gate"));
    for (String kind : Arrays.asList("http", "http_top")) {
      Map<String, Object> request = new LinkedHashMap<>();
      request.put("kind", kind); request.put("params", options); request.put("id", "gate-production-" + kind);
      request.put("allowMutation", false);
      runtime("captureOpen", request);
    }
    return result;
  }
  public void sample() throws Exception {
    profiles.add(Base64.getEncoder().encodeToString((byte[]) runtime("captureDump", Collections.singletonMap("id", "gate-production-http_top"))));
    Thread.sleep(25);
    profiles.add(Base64.getEncoder().encodeToString((byte[]) runtime("captureDump", Collections.singletonMap("id", "gate-production-http_top"))));
  }
  public Map<String, Object> finish() throws Exception {
    Map<String, Object> result = new LinkedHashMap<>();
    result.put("source", ObserverRuntime.SOURCE);
    List<Map<String, Object>> manual = new ArrayList<>(), official = new ArrayList<>();
    for (Map<String, Object> row : operations.drain())
      ("gate-http".equals(row.get("instrumentationName")) ? manual : official).add(row);
    result.put("operations", manual); result.put("officialOperations", official);
    result.put("spans", spans.drain());
    profiles.add(Base64.getEncoder().encodeToString((byte[]) runtime("captureDump", Collections.singletonMap("id", "gate-production-http_top"))));
    result.put("profiles", profiles);
    byte[] http = (byte[]) runtime("captureFinish", Collections.singletonMap("id", "gate-production-http"));
    result.put("http", new com.google.gson.Gson().fromJson(new String(http, java.nio.charset.StandardCharsets.UTF_8), List.class));
    for (String kind : Arrays.asList("http", "http_top"))
      runtime("captureClose", Collections.singletonMap("id", "gate-production-" + kind));
    result.put("productionReleased", !ObserverRuntime.armed("http") && !ObserverRuntime.armed("http_top"));
    spans.close(); operations.close();
    return result;
  }
  public int afterRows() { return operations.drain().size() + spans.drain().size(); }
  private static Object runtime(String action, Map<String, ?> request) throws Exception {
    return ManagementFactory.getPlatformMBeanServer().invoke(new ObjectName("com.flanksource.telltale:type=Diagnostics"),
        action, new Object[] {request}, new String[] {Map.class.getName()});
  }
}
