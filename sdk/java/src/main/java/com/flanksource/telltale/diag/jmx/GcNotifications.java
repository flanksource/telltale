package com.flanksource.telltale.diag.jmx;

import java.lang.management.ManagementFactory;
import java.lang.management.MemoryPoolMXBean;
import java.lang.management.MemoryType;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import javax.management.openmbean.CompositeData;
import javax.management.openmbean.TabularData;

/**
 * Decodes a {@code com.sun.management.gc.notification} — a collector reporting a finished
 * collection — into a flat map.
 *
 * <p>The user data is the CompositeData form of GarbageCollectionNotificationInfo, read by key
 * rather than through {@code com.sun.management} types so the WAR links on any JVM. Its start and
 * end times are milliseconds of JVM uptime; they are converted to epoch milliseconds with the
 * RuntimeMXBean start time so a reader can line a collection up against everything else it
 * captured.
 */
final class GcNotifications {

  /** The notification type every HotSpot collector emits on a finished collection. */
  static final String TYPE = "com.sun.management.gc.notification";

  private GcNotifications() {}

  /**
   * @return {collector, action, cause, id, startTime, endTime, durationMs, pools}, each pool {name,
   *     heap?, usedBefore, committedBefore, usedAfter, committedAfter}. {@code heap} is left out
   *     for a pool the JVM no longer lists, rather than guessed.
   * @throws IllegalArgumentException when the user data is not the documented shape.
   */
  static Map<String, Object> decode(Object userData) {
    if (!(userData instanceof CompositeData)) {
      throw new IllegalArgumentException(
          "GC notification user data is "
              + (userData == null ? "null" : userData.getClass().getName())
              + ", not CompositeData");
    }
    CompositeData info = (CompositeData) userData;
    CompositeData gcInfo = (CompositeData) info.get("gcInfo");
    long jvmStart = ManagementFactory.getRuntimeMXBean().getStartTime();
    Map<String, Object> gc = new LinkedHashMap<String, Object>();
    gc.put("collector", info.get("gcName"));
    gc.put("action", info.get("gcAction"));
    gc.put("cause", info.get("gcCause"));
    gc.put("id", gcInfo.get("id"));
    gc.put("startTime", Long.valueOf(jvmStart + ((Number) gcInfo.get("startTime")).longValue()));
    gc.put("endTime", Long.valueOf(jvmStart + ((Number) gcInfo.get("endTime")).longValue()));
    gc.put("durationMs", gcInfo.get("duration"));
    gc.put(
        "pools",
        pools(
            (TabularData) gcInfo.get("memoryUsageBeforeGc"),
            (TabularData) gcInfo.get("memoryUsageAfterGc")));
    return gc;
  }

  private static List<Map<String, Object>> pools(TabularData before, TabularData after) {
    Map<String, CompositeData> afterByPool = usageByPool(after);
    Map<String, MemoryType> types = poolTypes();
    List<Map<String, Object>> pools = new ArrayList<Map<String, Object>>();
    for (Map.Entry<String, CompositeData> entry : usageByPool(before).entrySet()) {
      CompositeData usedAfter = afterByPool.get(entry.getKey());
      if (usedAfter == null) {
        throw new IllegalArgumentException(
            "GC notification reports pool "
                + entry.getKey()
                + " before the collection and not after it");
      }
      Map<String, Object> pool = new LinkedHashMap<String, Object>();
      pool.put("name", entry.getKey());
      MemoryType type = types.get(entry.getKey());
      if (type != null) {
        pool.put("heap", Boolean.valueOf(type == MemoryType.HEAP));
      }
      pool.put("usedBefore", entry.getValue().get("used"));
      pool.put("committedBefore", entry.getValue().get("committed"));
      pool.put("usedAfter", usedAfter.get("used"));
      pool.put("committedAfter", usedAfter.get("committed"));
      pools.add(pool);
    }
    return pools;
  }

  /** A {@code Map<String, MemoryUsage>} in open-type form: rows of {key, value}. */
  private static Map<String, CompositeData> usageByPool(TabularData usage) {
    Map<String, CompositeData> byPool = new LinkedHashMap<String, CompositeData>();
    for (Object row : usage.values()) {
      CompositeData entry = (CompositeData) row;
      byPool.put(String.valueOf(entry.get("key")), (CompositeData) entry.get("value"));
    }
    return byPool;
  }

  private static Map<String, MemoryType> poolTypes() {
    Map<String, MemoryType> types = new HashMap<String, MemoryType>();
    for (MemoryPoolMXBean pool : ManagementFactory.getMemoryPoolMXBeans()) {
      types.put(pool.getName(), pool.getType());
    }
    return types;
  }
}
