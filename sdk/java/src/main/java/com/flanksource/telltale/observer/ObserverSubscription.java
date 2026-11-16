package com.flanksource.telltale.observer;

import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.ArrayBlockingQueue;

/** Each capture owns its queue, filters, budgets, and failure independently. */
public final class ObserverSubscription implements AutoCloseable {
  public final String kind;
  public final String id;
  public final Map<String, Object> params;
  private final ArrayBlockingQueue<Map<String, Object>> records;
  private final long maxBytes;
  private long bytes;
  private long dropped;
  private Throwable failure;
  private boolean closed;
  volatile boolean stopping;
  private final java.time.Instant armedAt = java.time.Instant.now();
  private final long leaseMillis;
  private volatile long expiresAt;
  private volatile boolean expired;
  private final long unmatchedAtStart = ObserverRuntime.unmatchedEnds();

  ObserverSubscription(String kind, String id, Map<String, Object> params) {
    this.kind = kind;
    this.id = id;
    this.params = Collections.unmodifiableMap(new LinkedHashMap<>(params));
    String capacityKey = params.containsKey("bufferSize") ? "bufferSize" : "capacity";
    Map<String, Object> limits = new LinkedHashMap<>(params);
    if (limits.get(capacityKey) instanceof Number && ((Number) limits.get(capacityKey)).intValue() == 0) limits.remove(capacityKey);
    records = new ArrayBlockingQueue<>(ObserverOptions.positiveInt(limits, capacityKey, 4096, 100000));
    maxBytes = ObserverOptions.positiveInt(params, "bufferBytes", 8 << 20, 64 << 20);
    leaseMillis = ObserverOptions.positiveInt(params, "leaseMillis", 120000, 120000);
    expiresAt = Long.MAX_VALUE;
  }

  synchronized void emit(Map<String, Object> source, boolean terminal) {
    if (closed || failure != null || stopping && !terminal || !matches(source)) return;
    if (!kind.equals("spans")) {
      Object started = source.containsKey("startedAt") ? source.get("startedAt") : source.get("startedDateTime");
      if (started instanceof String && java.time.Instant.parse((String) started).isBefore(armedAt)) return;
    }
    Map<String, Object> row = ObserverRuntime.project(kind, params, new LinkedHashMap<>(source));
    if (row == null) return;
    if (!kind.equals("http") && !Boolean.TRUE.equals(params.get("body"))) {
      row.remove("body"); row.remove("requestBody"); row.remove("responseBody");
    }
    int size = ObserverRuntime.json(row).getBytes(StandardCharsets.UTF_8).length;
    if (size > maxBytes - bytes || !records.offer(row)) { dropped++; return; }
    bytes += size;
  }

  private boolean matches(Map<String, Object> row) {
    Object direction = params.get("direction");
    if (direction != null && !"".equals(direction) && !"both".equals(direction) && !direction.equals(row.get("direction")))
      return false;
    Object minimum = params.get("minDurationMs");
    return !(minimum instanceof Number) || !(row.get("durationMs") instanceof Number)
        || ((Number) row.get("durationMs")).doubleValue() >= ((Number) minimum).doubleValue();
  }

  public synchronized List<Map<String, Object>> drain() {
    check();
    List<Map<String, Object>> batch = new ArrayList<>();
    records.drainTo(batch);
    bytes = 0;
    return batch;
  }

  public synchronized long dropped() { return dropped; }
  public boolean expired() { return expired; }
  public Map<String, Object> report() {
    Map<String, Object> result = new LinkedHashMap<>();
    result.put("unmatchedPreAttachmentEnds", ObserverRuntime.unmatchedEnds() - unmatchedAtStart);
    result.put("leaseExpired", expired);
    return result;
  }
  void startLease() { expiresAt = System.nanoTime() + java.util.concurrent.TimeUnit.MILLISECONDS.toNanos(leaseMillis); }
  boolean overdue(long now) { return !stopping && now >= expiresAt; }
  public synchronized void renew() {
    check();
    if (closed || stopping || System.nanoTime() >= expiresAt)
      throw new IllegalStateException("observer capture " + id + " lease has expired or stopped");
    expiresAt = System.nanoTime() + java.util.concurrent.TimeUnit.MILLISECONDS.toNanos(leaseMillis);
  }
  void expire() {
    IllegalStateException error = new IllegalStateException("observer capture " + id + " lease expired");
    synchronized (this) {
      if (closed || stopping) return;
      expired = true;
      fail(error);
    }
    try { close(); } catch (Exception cleanup) { error.addSuppressed(cleanup); }
    synchronized (this) { records.clear(); bytes = 0; }
  }
  public synchronized void fail(Throwable error) { if (failure == null) failure = error; }
  public synchronized void check() {
    if (failure != null) throw new IllegalStateException("observer capture " + id + " failed", failure);
  }
  public List<Map<String, Object>> snapshot() {
    check();
    List<Map<String, Object>> projected = new ArrayList<>();
    for (Map<String, Object> row : ObserverRuntime.snapshot(kind)) {
      Map<String, Object> selected = ObserverRuntime.project(kind, params, row);
      if (selected != null) projected.add(selected);
    }
    return projected;
  }
  public void close() throws Exception {
    synchronized (this) { if (closed || stopping) return; stopping = true; }
    try { ObserverRuntime.release(this); }
    finally { synchronized (this) { closed = true; } }
  }
}
