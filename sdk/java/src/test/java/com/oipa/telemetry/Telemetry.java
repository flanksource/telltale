package com.oipa.telemetry;

import java.util.*;

public final class Telemetry {
  public static ClassLoader context;
  public static boolean fail;

  public static Map<String, Object> snapshot() {
    context = Thread.currentThread().getContextClassLoader();
    if (fail) throw new IllegalStateException("telemetry fixture failure");
    Map<String, Object> status = new LinkedHashMap<>();
    status.put("armed", true);
    status.put("mode", "boot");
    status.put("seriesCount", 1);
    status.put("errors", 0L);
    Map<String, Object> histogram = new LinkedHashMap<>();
    histogram.put("name", "oipa_activity_processing_seconds");
    histogram.put("labels", Collections.singletonMap("transaction", "Premium \"monthly\""));
    histogram.put("count", 9007199254740993L);
    histogram.put("sumSeconds", 0.025);
    histogram.put("buckets", Collections.singletonMap("+Inf", 9007199254740993L));
    Map<String, Object> result = new LinkedHashMap<>();
    result.put("status", status);
    result.put("histograms", Collections.singletonList(histogram));
    return result;
  }

  public static void main(String[] args) throws Exception {
    snapshot();
    System.out.println("ready " + java.lang.management.ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
    while (System.in.read() != -1) {}
  }
}
