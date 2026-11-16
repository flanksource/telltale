package observerfixture.springhttp;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverSubscription;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;

public final class SpringHttpChecks {
  private SpringHttpChecks() {}

  public static Map<String, Object> options() {
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("direction", "both");
    options.put("bodies", "64KiB");
    return options;
  }

  public static List<Map<String, Object>> completed(ObserverSubscription capture, int count) throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    await(() -> { rows.addAll(capture.drain()); return rows.size() >= count; });
    assertEquals("each operation completes once", count, rows.size());
    return rows;
  }

  public static void await(BooleanSupplier condition) throws Exception {
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
    while (!condition.getAsBoolean()) {
      if (System.nanoTime() >= deadline) fail("HTTP lifecycle condition did not become true within 10 seconds");
      Thread.sleep(10);
    }
  }

  @SuppressWarnings("unchecked")
  public static Map<String, Object> attributes(Map<String, Object> row) {
    return (Map<String, Object>) row.get("attributes");
  }

  public static Set<Object> identities(List<Map<String, Object>> rows, String field) {
    Set<Object> ids = new HashSet<>();
    for (Map<String, Object> row : rows) assertTrue("operation identity must be unique", ids.add(row.get(field)));
    assertFalse("operation identity must be present", ids.contains(null));
    return ids;
  }
}
