package com.flanksource.telltale.diag.jvm;

import java.lang.instrument.Instrumentation;
import java.lang.reflect.InvocationTargetException;
import java.time.Instant;
import java.util.*;
import com.flanksource.telltale.diag.hotpatch.ClassIndex;

public final class NativeTelemetryService {
  private static final String TELEMETRY = "com.oipa.telemetry.Telemetry";
  private final Instrumentation instrumentation;

  public NativeTelemetryService(Instrumentation instrumentation) {
    this.instrumentation = Objects.requireNonNull(instrumentation, "instrumentation");
  }

  public Map<String, Object> snapshot() throws Exception {
    List<Map<String, Object>> applications = new ArrayList<>();
    for (Class<?> type : instrumentation.getAllLoadedClasses()) {
      if (!type.getName().equals(TELEMETRY)) continue;
      ClassLoader caller = Thread.currentThread().getContextClassLoader();
      try {
        Thread.currentThread().setContextClassLoader(type.getClassLoader());
        Map<?, ?> snapshot = read(type);
        Map<String, Object> application = new LinkedHashMap<>();
        application.put("loader", ClassIndex.hash(type.getClassLoader()));
        application.put("label", ClassIndex.label(type.getClassLoader()));
        application.put("status", snapshot.get("status"));
        application.put("histograms", snapshot.get("histograms"));
        applications.add(application);
      } finally {
        Thread.currentThread().setContextClassLoader(caller);
      }
    }
    applications.sort(Comparator.comparing(row -> (String) row.get("loader")));
    Map<String, Object> result = new LinkedHashMap<>();
    result.put("capturedAt", Instant.now().toString());
    result.put("applications", applications);
    return result;
  }

  private Map<?, ?> read(Class<?> type) throws Exception {
    Object value;
    try {
      value = type.getMethod("snapshot").invoke(null);
    } catch (NoSuchMethodException error) {
      throw new IllegalStateException(TELEMETRY + " in loader " + ClassIndex.hash(type.getClassLoader())
          + " has no native snapshot API; update the telemetry extension and restart the JVM", error);
    } catch (InvocationTargetException error) {
      if (error.getCause() instanceof Error) throw (Error) error.getCause();
      if (error.getCause() instanceof Exception) throw (Exception) error.getCause();
      throw new IllegalStateException("native telemetry snapshot failed", error.getCause());
    }
    if (!(value instanceof Map)) throw new IllegalStateException("native telemetry snapshot must be an object");
    Map<?, ?> snapshot = (Map<?, ?>) value;
    if (!(snapshot.get("status") instanceof Map) || !(snapshot.get("histograms") instanceof List))
      throw new IllegalStateException("native telemetry snapshot must contain status and histograms");
    Map<?, ?> status = (Map<?, ?>) snapshot.get("status");
    if (!(status.get("armed") instanceof Boolean) || !(status.get("mode") instanceof String))
      throw new IllegalStateException("native telemetry status must contain armed and mode");
    return snapshot;
  }
}
