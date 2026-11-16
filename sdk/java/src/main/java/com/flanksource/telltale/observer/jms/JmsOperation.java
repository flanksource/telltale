package com.flanksource.telltale.observer.jms;

import java.time.Instant;
import java.lang.ref.WeakReference;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;

final class JmsOperation {
  final String operation;
  final long startedNanos = System.nanoTime();
  final Map<String, Object> row = new LinkedHashMap<>();
  final AtomicBoolean ended = new AtomicBoolean();
  final List<String> metadataErrors = new ArrayList<>();
  private volatile WeakReference<Object> message = new WeakReference<>(null);
  boolean asynchronous;
  boolean bodyInspected;
  private String failureType;
  private String failureMessage;
  String phase;

  JmsOperation(String operation) {
    this.operation = operation;
    row.put("startedAt", Instant.now().toString());
    row.put("operation", operation);
    row.put("direction", operation.equals("send") ? "outgoing" : "incoming");
    phase = operation.equals("process") ? "handler" : operation;
  }

  synchronized void put(String name, Object value) { if (value != null) row.put(name, value); }

  Object message() { return message.get(); }
  void message(Object value) { message = new WeakReference<>(value); }

  synchronized void failed(Throwable error, String phase) {
    failureType = error.getClass().getName();
    failureMessage = error.getMessage() == null ? failureType : error.getMessage();
    this.phase = phase;
  }

  synchronized void metadataError(String value) {
    if (metadataErrors.size() < 16 && !metadataErrors.contains(value)) metadataErrors.add(value);
  }

  synchronized Map<String, Object> finish(Throwable error, String outcome, int bodyLimit) {
    if (!ended.compareAndSet(false, true)) return null;
    if (!(asynchronous && outcome.equals("incomplete"))) JmsAccess.message(this, bodyLimit);
    row.put("durationMs", (System.nanoTime() - startedNanos) / 1000000.0);
    if (error != null) failed(error, phase);
    row.put("outcome", failureType == null ? outcome : "error");
    if (failureType != null) {
      row.put("error", failureMessage);
      row.put("errorType", failureType);
      row.put("phase", phase);
    }
    if (!metadataErrors.isEmpty()) row.put("metadataErrors", new ArrayList<>(metadataErrors));
    message.clear();
    return new LinkedHashMap<>(row);
  }

  synchronized Map<String, Object> incomplete() {
    if (ended.get()) return null;
    Map<String, Object> snapshot = new LinkedHashMap<>(row);
    snapshot.put("durationMs", (System.nanoTime() - startedNanos) / 1000000.0);
    snapshot.put("outcome", "incomplete");
    if (failureType != null) {
      snapshot.put("error", failureMessage);
      snapshot.put("errorType", failureType);
      snapshot.put("phase", phase);
    }
    List<String> errors = new ArrayList<>(metadataErrors);
    errors.add("Capture ended before JMS operation completion");
    snapshot.put("metadataErrors", errors);
    return snapshot;
  }
}
