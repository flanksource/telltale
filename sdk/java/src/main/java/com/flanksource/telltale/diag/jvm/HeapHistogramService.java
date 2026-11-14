package com.flanksource.telltale.diag.jvm;

import java.lang.management.ManagementFactory;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import javax.management.ObjectName;

/**
 * The heap's class histogram — {@code jcmd <pid> GC.class_histogram} — read through the {@code
 * DiagnosticCommand} MBean and parsed here, so a client receives rows rather than text it would have
 * to parse per JDK.
 *
 * <p>{@code live:false}, the default, passes {@code -all}: every object in the heap, reachable or
 * not, with no collection forced. {@code live:true} counts reachable objects only, which the JVM
 * answers by running a FULL GC first — a stop-the-world pause on the JVM being diagnosed, so it is
 * never the default.
 *
 * <p>{@code classPattern} is a glob over the binary class name ({@code *} any run, {@code ?} one
 * character, everything else literal): class names are full of dots and dollars, which a regex would
 * make every caller escape.
 */
public final class HeapHistogramService {

  static final int DEFAULT_TOP = 100;

  private static final Pattern ROW =
      Pattern.compile("^\\s*(\\d+):\\s+(\\d+)\\s+(\\d+)\\s+(\\S+)(?:\\s+\\((.*)\\))?\\s*$");
  private static final Pattern TOTAL = Pattern.compile("^Total\\s+(\\d+)\\s+(\\d+)\\s*$");

  public Map<String, Object> histogram(Map<String, Object> params) throws Exception {
    int top = top(params.get("top"));
    Object pattern = params.get("classPattern");
    if (pattern != null && !(pattern instanceof String)) {
      throw new IllegalArgumentException("classPattern must be a string glob, got " + pattern);
    }
    Object live = params.get("live");
    if (live != null && !(live instanceof Boolean)) {
      throw new IllegalArgumentException("live must be a boolean, got " + live);
    }
    boolean liveOnly = Boolean.TRUE.equals(live);
    String[] args = liveOnly ? new String[0] : new String[] {"-all"};
    Object output =
        ManagementFactory.getPlatformMBeanServer()
            .invoke(
                new ObjectName("com.sun.management:type=DiagnosticCommand"),
                "gcClassHistogram",
                new Object[] {args},
                new String[] {String[].class.getName()});
    return parse(String.valueOf(output), top, (String) pattern, liveOnly);
  }

  private static int top(Object raw) {
    if (raw == null) {
      return DEFAULT_TOP;
    }
    if (!(raw instanceof Number) || ((Number) raw).intValue() <= 0) {
      throw new IllegalArgumentException("top must be a positive integer, got " + raw);
    }
    return ((Number) raw).intValue();
  }

  /**
   * Parses {@code GC.class_histogram} output.
   *
   * @throws IllegalStateException when it has no Total line: whatever the JVM printed instead (a
   *     refusal, a changed format) is reported, never an empty heap
   */
  static Map<String, Object> parse(String output, int top, String classPattern, boolean live) {
    Pattern glob = classPattern == null || classPattern.isEmpty() ? null : glob(classPattern);
    List<Map<String, Object>> rows = new ArrayList<Map<String, Object>>();
    Long totalInstances = null;
    Long totalBytes = null;
    for (String line : output.split("\\r?\\n")) {
      Matcher total = TOTAL.matcher(line);
      if (total.matches()) {
        totalInstances = Long.valueOf(total.group(1));
        totalBytes = Long.valueOf(total.group(2));
        continue;
      }
      Matcher row = ROW.matcher(line);
      if (row.matches() && (glob == null || glob.matcher(row.group(4)).matches())) {
        rows.add(row(row));
      }
    }
    if (totalBytes == null) {
      throw new IllegalStateException(
          "GC.class_histogram returned no Total line; it said: " + head(output));
    }
    Collections.sort(rows, BY_BYTES);
    Map<String, Object> histogram = new LinkedHashMap<String, Object>();
    histogram.put("collectedAt", java.time.Instant.now().toString());
    histogram.put("live", Boolean.valueOf(live));
    histogram.put("totalInstances", totalInstances);
    histogram.put("totalBytes", totalBytes);
    histogram.put("matched", Integer.valueOf(rows.size()));
    histogram.put("rows", new ArrayList<Map<String, Object>>(rows.subList(0, Math.min(top, rows.size()))));
    return histogram;
  }

  private static Map<String, Object> row(Matcher row) {
    Map<String, Object> out = new LinkedHashMap<String, Object>();
    out.put("rank", Integer.valueOf(row.group(1)));
    out.put("className", row.group(4));
    out.put("instances", Long.valueOf(row.group(2)));
    out.put("bytes", Long.valueOf(row.group(3)));
    if (row.group(5) != null) {
      out.put("loader", row.group(5));
    }
    return out;
  }

  private static final Comparator<Map<String, Object>> BY_BYTES =
      new Comparator<Map<String, Object>>() {
        @Override
        public int compare(Map<String, Object> a, Map<String, Object> b) {
          return Long.compare((Long) b.get("bytes"), (Long) a.get("bytes"));
        }
      };

  static Pattern glob(String glob) {
    StringBuilder regex = new StringBuilder();
    for (char c : glob.toCharArray()) {
      if (c == '*') {
        regex.append(".*");
      } else if (c == '?') {
        regex.append('.');
      } else {
        regex.append(Pattern.quote(String.valueOf(c)));
      }
    }
    return Pattern.compile(regex.toString());
  }

  private static String head(String output) {
    String trimmed = output.trim();
    return trimmed.length() <= 500 ? trimmed : trimmed.substring(0, 500) + "…";
  }
}
