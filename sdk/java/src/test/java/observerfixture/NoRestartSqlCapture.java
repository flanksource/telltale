package observerfixture;

import com.flanksource.telltale.observer.ObserverRuntime;
import java.lang.management.ManagementFactory;
import java.nio.charset.StandardCharsets;
import java.util.*;
import javax.management.ObjectName;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

/** Uses the packaged agent's production MBean capture and Profiles encoder. */
public final class NoRestartSqlCapture {
  private NoRestartSqlCapture() {}

  public static Map<String, Object> run() throws Exception {
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("connections", Collections.singletonList("*"));
    params.put("minDurationNs", 0L);
    params.put("pollIntervalMs", 5000);
    params.put("limit", 50);
    for (String kind : Arrays.asList("sql", "sql_top")) {
      Map<String, Object> request = new LinkedHashMap<>();
      request.put("kind", kind); request.put("params", params); request.put("id", "gate-production-" + kind);
      request.put("allowMutation", false);
      runtime("captureOpen", request);
    }
    Map<String, Object> result = new LinkedHashMap<>();
    try {
      if (!NoRestartSqlState.jdbc.execute()) throw new IllegalStateException("observed JDBC result changed");
      List<? extends io.r2dbc.spi.Result> nativeResults = Flux.from(NoRestartSqlState.reactive.execute()).collectList().block();
      List<String> profiles = new ArrayList<>();
      profiles.add(dump());
      Thread.sleep(25);
      profiles.add(dump());
      if (!Integer.valueOf(1).equals(Mono.from(nativeResults.get(0).map((row, metadata) -> row.get(0, Integer.class))).block()))
        throw new IllegalStateException("observed R2DBC result changed");
      profiles.add(dump());
      result.put("sqlProfiles", profiles);
      byte[] records = (byte[]) runtime("captureFinish", Collections.singletonMap("id", "gate-production-sql"));
      result.put("sqlRows", new com.google.gson.Gson().fromJson(new String(records, StandardCharsets.UTF_8), List.class));
      result.put("sqlSource", ObserverRuntime.SOURCE);
    } finally {
      for (String kind : Arrays.asList("sql", "sql_top"))
        runtime("captureClose", Collections.singletonMap("id", "gate-production-" + kind));
    }
    result.put("sqlReleased", !ObserverRuntime.armed("sql") && !ObserverRuntime.armed("sql_top"));
    return result;
  }

  private static String dump() throws Exception {
    return Base64.getEncoder().encodeToString((byte[]) runtime("captureDump", Collections.singletonMap("id", "gate-production-sql_top")));
  }

  private static Object runtime(String action, Map<String, ?> request) throws Exception {
    return ManagementFactory.getPlatformMBeanServer().invoke(new ObjectName("com.flanksource.telltale:type=Diagnostics"),
        action, new Object[] {request}, new String[] {Map.class.getName()});
  }
}
