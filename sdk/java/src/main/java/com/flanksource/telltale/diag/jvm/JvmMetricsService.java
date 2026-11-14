package com.flanksource.telltale.diag.jvm;

import com.flanksource.telltale.diag.jmx.JmxService;
import java.lang.management.BufferPoolMXBean;
import java.lang.management.GarbageCollectorMXBean;
import java.lang.management.ManagementFactory;
import java.lang.management.MemoryPoolMXBean;
import java.lang.management.MemoryType;
import java.lang.management.MemoryUsage;
import java.lang.management.RuntimeMXBean;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import javax.management.ObjectName;

/** Captures the target JVM metrics used by the dashboard and timeseries sampler. */
public final class JvmMetricsService {

  private final JmxService jmx;

  public JvmMetricsService() {
    this(new JmxService());
  }

  JvmMetricsService(JmxService jmx) {
    this.jmx = jmx;
  }

  public Map<String, Object> snapshot() throws Exception {
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("capturedAt", Instant.now().toString());
    out.put("memory", memory());
    out.put("garbageCollectors", garbageCollectors());
    out.put("runtime", runtime());
    Object processCPU = processCPUPercent();
    if (processCPU != null) out.put("processCpuPercent", processCPU);

    List<Map<String, Object>> connectors = connectorMetrics();
    List<Map<String, Object>> dataSources = dataSourceMetrics();
    out.put("connectors", connectors);
    out.put("dataSources", dataSources);

    Map<String, Object> series = new LinkedHashMap<>();
    series.put("collectors", names(garbageCollectors(), "name"));
    series.put("connectors", names(connectors, "name"));
    series.put("dataSources", names(dataSources, "name"));
    out.put("series", series);
    return out;
  }

  private static Map<String, Object> memory() {
    List<Map<String, Object>> heap = new ArrayList<>();
    List<Map<String, Object>> nonHeap = new ArrayList<>();
    for (MemoryPoolMXBean pool : ManagementFactory.getMemoryPoolMXBeans()) {
      Map<String, Object> row = memoryRow(pool.getName(), pool.getType().name(), pool.getUsage());
      if (pool.getType() == MemoryType.HEAP) heap.add(row);
      else nonHeap.add(row);
    }
    List<Map<String, Object>> buffers = new ArrayList<>();
    for (BufferPoolMXBean pool : ManagementFactory.getPlatformMXBeans(BufferPoolMXBean.class)) {
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", pool.getName());
      row.put("type", "BUFFER");
      row.put("used", pool.getMemoryUsed());
      row.put("committed", pool.getTotalCapacity());
      row.put("max", -1L);
      buffers.add(row);
    }
    Map<String, Object> memory = new LinkedHashMap<>();
    memory.put("heap", heap);
    memory.put("nonHeap", nonHeap);
    memory.put("buffer", buffers);
    return memory;
  }

  private static Map<String, Object> memoryRow(String name, String type, MemoryUsage usage) {
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("name", name);
    row.put("type", type);
    row.put("used", usage == null ? 0L : usage.getUsed());
    row.put("committed", usage == null ? 0L : usage.getCommitted());
    row.put("max", usage == null ? -1L : usage.getMax());
    return row;
  }

  private static List<Map<String, Object>> garbageCollectors() {
    List<Map<String, Object>> collectors = new ArrayList<>();
    for (GarbageCollectorMXBean collector : ManagementFactory.getGarbageCollectorMXBeans()) {
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", collector.getName());
      row.put("collectionCount", collector.getCollectionCount());
      row.put("collectionTime", collector.getCollectionTime());
      collectors.add(row);
    }
    return collectors;
  }

  private static Map<String, Object> runtime() {
    RuntimeMXBean runtime = ManagementFactory.getRuntimeMXBean();
    java.lang.management.OperatingSystemMXBean os = ManagementFactory.getOperatingSystemMXBean();
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("javaVersion", System.getProperty("java.version"));
    out.put("javaHome", System.getProperty("java.home"));
    out.put("osName", os.getName());
    out.put("osVersion", os.getVersion());
    out.put("processors", os.getAvailableProcessors());
    out.put("systemLoadAverage", os.getSystemLoadAverage());
    out.put("uptime", runtime.getUptime());
    return out;
  }

  private Object processCPUPercent() {
    try {
      Object value = jmx.read("java.lang:type=OperatingSystem", "ProcessCpuLoad");
      if (value instanceof Number && ((Number) value).doubleValue() >= 0) {
        return ((Number) value).doubleValue() * 100D;
      }
    } catch (Exception ignored) {
      // ProcessCpuLoad is not present on every supported JVM.
    }
    return null;
  }

  private List<Map<String, Object>> connectorMetrics() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    for (String name : jmx.search("Catalina:type=ThreadPool,*")) {
      rows.add(metricRow(name, Arrays.asList("currentThreadsBusy", "maxThreads")));
    }
    return rows;
  }

  private List<Map<String, Object>> dataSourceMetrics() throws Exception {
    List<String> names = new ArrayList<>();
    names.addAll(jmx.search("com.zaxxer.hikari:type=Pool (*)"));
    names.addAll(jmx.search("openejb.management:ObjectType=datasources,*"));
    names.addAll(jmx.search("*:type=ConnectionPool,*"));
    List<Map<String, Object>> rows = new ArrayList<>();
    for (String name : names) {
      if (name.contains("connections=")) continue;
      rows.add(
          metricRow(
              name,
              Arrays.asList(
                  "ActiveConnections",
                  "IdleConnections",
                  "TotalConnections",
                  "Active",
                  "Idle",
                  "Size")));
    }
    return rows;
  }

  private Map<String, Object> metricRow(String objectName, List<String> wanted) throws Exception {
    @SuppressWarnings("unchecked")
    List<Map<String, Object>> attributes =
        (List<Map<String, Object>>) jmx.readAll(objectName).get("attributes");
    Map<String, Object> row = new LinkedHashMap<>();
    ObjectName parsed = new ObjectName(objectName);
    String label = parsed.getKeyProperty("name");
    if (label == null) label = parsed.getKeyProperty("DataSource");
    row.put("name", label == null ? parsed.getCanonicalName() : label);
    row.put("objectName", parsed.getCanonicalName());
    for (Map<String, Object> attribute : attributes) {
      String name = String.valueOf(attribute.get("name"));
      if (wanted.contains(name)) row.put(name, attribute.get("value"));
    }
    return row;
  }

  private static List<String> names(List<Map<String, Object>> rows, String field) {
    List<String> out = new ArrayList<>();
    for (Map<String, Object> row : rows) {
      Object value = row.get(field);
      if (value != null) out.add(String.valueOf(value));
    }
    return out;
  }
}
