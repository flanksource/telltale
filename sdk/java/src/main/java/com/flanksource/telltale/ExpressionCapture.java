package com.flanksource.telltale;

import java.nio.charset.StandardCharsets;
import java.util.*;

final class ExpressionCapture implements Capture {
  private final Map<String, Object> params;
  private final ClassLoader loader;
  private final long interval;
  private long next;
  private boolean closed;

  ExpressionCapture(Map<String, Object> params, ClassLoader loader) {
    Diagnostics.required(params, "expression");
    this.params = new LinkedHashMap<>(params);
    this.loader = loader;
    double millis = ((Number) params.getOrDefault("intervalMs", 1000)).doubleValue();
    if (millis < 100 || millis > 3600000 || millis != Math.rint(millis))
      throw new IllegalArgumentException("intervalMs must be an integer between 100 and 3600000");
    interval = (long) millis;
  }

  public synchronized byte[] dump() throws Exception {
    if (closed) throw new IllegalStateException("expression capture is closed");
    long now = System.currentTimeMillis();
    if (now < next) return "[]".getBytes(StandardCharsets.UTF_8);
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("timestamp", java.time.Instant.now().toString());
    row.put("expression", params.get("expression"));
    row.put("result", OgnlEvaluation.evaluate(params, loader));
    next = now + interval;
    return Agent.JSON.toJson(Collections.singletonList(row)).getBytes(StandardCharsets.UTF_8);
  }

  public synchronized void close() {
    closed = true;
  }

  public boolean streaming() {
    return true;
  }

  public String contentType() {
    return "application/json";
  }

  public String fileName() {
    return "expressions.json";
  }
}
