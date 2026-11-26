package com.flanksource.telltale;

import com.flanksource.telltale.diag.hotpatch.*;
import java.nio.charset.StandardCharsets;
import java.util.*;

final class MethodCapture implements Capture {
  private final ClassOperations classes;
  private final String key;
  private final List<Map<String, Object>> targets = new ArrayList<>();
  private final boolean redefine;
  private long cursor;
  private boolean closed;
  private boolean complete;
  private boolean more;
  private final List<Map<String, Object>> results = new ArrayList<>();
  private final boolean filtered;
  private long filteredCalls;
  private long omittedEvents;
  private long probeDropped;
  private boolean instrumented;

  @SuppressWarnings("unchecked")
  MethodCapture(ClassOperations classes, String kind, Map<String, Object> params, String key)
      throws Exception {
    this.classes = classes;
    this.key = key;
    this.redefine = kind.equals("jvm_redefine");
    this.filtered = !redefine && ClassOperations.filterOptions(params) != null;
    Object selected = redefine ? Collections.singletonList(params) : params.get("targets");
    if (!(selected instanceof List)
        || ((List<?>) selected).isEmpty()
        || ((List<?>) selected).size() > 8)
      throw new IllegalArgumentException("targets must contain between one and eight methods");
    try {
      synchronized (classes) {
        for (Object raw : (List<?>) selected) {
          if (!(raw instanceof Map))
            throw new IllegalArgumentException("each target must be an object");
          Map<String, Object> target = new LinkedHashMap<>((Map<String, Object>) raw);
          target.put("leaseSeconds", 120);
          target.put("managedCapture", true);
          if (redefine) {
            target.put("leaseId", UUID.randomUUID().toString());
            target.put("source", Diagnostics.required(params, "source"));
            Map<String, Object> patched = classes.patch(new JsonBody(target));
            if (!Boolean.TRUE.equals(patched.get("applied")))
              throw new IllegalArgumentException("compilation failed: " + patched);
            target.put("loader", patched.get("loader"));
            Map<String, Object> result = new LinkedHashMap<>(patched);
            result.put("class", target.get("class"));
            result.put("applied", true);
            results.add(result);
          } else {
            Map<String, Object> options = new LinkedHashMap<>(params);
            options.remove("targets");
            options.remove("target");
            options.put("key", key);
            options.put("join", !targets.isEmpty());
            target.put("probe", options);
            Map<String, Object> armed = classes.instrument(new JsonBody(target));
            target.put("loader", armed.get("loader"));
            instrumented |= Boolean.TRUE.equals(armed.get("instrumented"));
          }
          targets.add(target);
        }
      }
    } catch (Exception error) {
      try {
        close();
      } catch (Exception cleanup) {
        error.addSuppressed(cleanup);
      }
      throw error;
    }
  }

  public synchronized byte[] dump() throws Exception {
    if (closed) throw new IllegalStateException("capture is closed");
    if (redefine) return patchResults(false);
    Map<String, Object> batch = AgentBridge.drain(key, cursor, 10000);
    if (batch.containsKey("error"))
      throw new IllegalStateException(String.valueOf(batch.get("error")));
    if (Boolean.TRUE.equals(batch.get("closed")))
      throw new IllegalStateException("probe was closed before capture finished: " + key);
    List<?> lines = (List<?>) batch.get("events");
    List<Object> events = new ArrayList<>();
    long lost = ((Number) batch.get("dropped")).longValue();
    probeDropped += lost;
    if (lost > 0) {
      Map<String, Object> loss = new LinkedHashMap<>();
      loss.put("kind", "dropped");
      loss.put("key", key);
      loss.put("ts", System.currentTimeMillis() * 1000000L);
      loss.put("dropped", lost);
      events.add(loss);
    }
    for (Object line : lines) {
      com.google.gson.JsonObject event =
          Agent.JSON.fromJson((String) line, com.google.gson.JsonObject.class);
      event.add("probeSeq", event.remove("seq"));
      events.add(event);
    }
    filteredCalls = ((Number) batch.get("filtered")).longValue();
    omittedEvents = ((Number) batch.get("omitted")).longValue();
    long next = ((Number) batch.get("nextSeq")).longValue();
    more = next < ((Number) batch.get("writeSeq")).longValue();
    complete =
        Boolean.TRUE.equals(batch.get("complete"))
            && next >= ((Number) batch.get("writeSeq")).longValue();
    byte[] result = Agent.JSON.toJson(events).getBytes(StandardCharsets.UTF_8);
    cursor = next;
    return result;
  }

  public synchronized void renew() throws Exception {
    if (closed) return;
    synchronized (classes) {
      for (Map<String, Object> target : targets) {
        Map<String, Object> renewal = new LinkedHashMap<>(target);
        renewal.put("seconds", 120);
        if (!Boolean.TRUE.equals(classes.lease(new JsonBody(renewal)).get("patched")))
          throw new IllegalStateException("capture lost its patch on " + target.get("class"));
      }
    }
  }

  public synchronized void close() throws Exception {
    if (closed) return;
    Exception failure = null;
    synchronized (classes) {
      ListIterator<Map<String, Object>> iterator = targets.listIterator(targets.size());
      while (iterator.hasPrevious()) {
        try {
          classes.restore(new JsonBody(iterator.previous()));
          iterator.remove();
        } catch (Exception error) {
          if (failure == null) failure = error;
          else failure.addSuppressed(error);
        }
      }
    }
    closed = targets.isEmpty();
    if (failure != null) throw failure;
  }

  /** Instrumentation, release, and the loss counters of the method probe. */
  public synchronized Map<String, Object> report() {
    if (redefine) return null;
    Map<String, Object> report = new LinkedHashMap<>();
    report.put("instrumented", instrumented);
    report.put("restored", instrumented && closed);
    report.put("probeDropped", probeDropped);
    if (filtered) {
      report.put("filtered", filteredCalls);
      report.put("omitted", omittedEvents);
    }
    return report;
  }

  public String contentType() {
    return "application/json";
  }

  public synchronized byte[] finish() throws Exception {
    if (!redefine) {
      freeze();
      com.google.gson.JsonArray events = new com.google.gson.JsonArray();
      do {
        events.addAll(
            Agent.JSON.fromJson(
                new String(dump(), StandardCharsets.UTF_8), com.google.gson.JsonArray.class));
      } while (more);
      return Agent.JSON.toJson(events).getBytes(StandardCharsets.UTF_8);
    }
    close();
    return patchResults(true);
  }

  private byte[] patchResults(boolean restored) {
    for (Map<String, Object> result : results) {
      result.put("restored", restored);
      result.put("timestamp", java.time.Instant.now().toString());
    }
    return Agent.JSON.toJson(results).getBytes(StandardCharsets.UTF_8);
  }

  public void freeze() {
    if (!redefine) AgentBridge.freezeProbe(key);
  }

  public String fileName() {
    return "events.json";
  }

  public boolean streaming() {
    return !redefine;
  }

  public synchronized boolean complete() {
    return complete;
  }
}
