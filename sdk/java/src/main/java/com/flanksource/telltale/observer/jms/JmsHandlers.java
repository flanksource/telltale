package com.flanksource.telltale.observer.jms;

import java.util.*;

final class JmsHandlers {
  private final Map<String, Entry> entries = new LinkedHashMap<>();
  private final Map<JmsOperation, Entry> active = new IdentityHashMap<>();

  synchronized void enter(JmsOperation operation) {
    String key = key(operation);
    Entry entry = entries.get(key);
    if (entry == null) {
      if (entries.size() >= 8192) throw new IllegalStateException("JMS handler mapping limit exceeded");
      entry = new Entry(); entries.put(key, entry);
    }
    entry.observe(operation);
    active.put(operation, entry);
  }

  synchronized void exit(JmsOperation operation) {
    Entry entry = active.remove(operation);
    if (entry != null) entry.attributes.putAll(operation.row);
  }
  synchronized void refresh(JmsOperation operation) {
    Entry entry = active.get(operation);
    if (entry != null) entry.attributes.putAll(operation.row);
  }
  synchronized void discover(JmsOperation operation) {
    String key = key(operation);
    if (!entries.containsKey(key)) { enter(operation); exit(operation); }
    else entries.get(key).register(operation.handlerOwner);
  }

  synchronized List<Map<String,Object>> snapshot() {
    entries.values().removeIf(entry -> entry.prune() && !active.containsValue(entry));
    List<Map<String,Object>> rows = new ArrayList<>();
    long sampled = com.flanksource.telltale.observer.ObserverRuntime.epochNanos();
    long now = System.nanoTime();
    for (Entry entry : entries.values()) {
      long elapsed = 0; int count = 0;
      for (Map.Entry<JmsOperation,Entry> running : active.entrySet()) if (running.getValue() == entry) {
        count++; elapsed = Math.max(elapsed, now - running.getKey().startedNanos);
      }
      Map<String,Object> attributes = new LinkedHashMap<>(entry.attributes);
      attributes.remove("body"); attributes.remove("messageId"); attributes.remove("correlationId");
      attributes.put("activeCount", count); attributes.put("state", count == 0 ? "idle" : "busy");
      Map<String,Object> row = new LinkedHashMap<>();
      row.put("operationId", entry.id); row.put("elapsedNs", elapsed);
      row.put("sampledAtNanos", sampled); row.put("attributes", attributes); rows.add(row);
    }
    return rows;
  }

  synchronized void clear() { entries.clear(); active.clear(); }
  synchronized void remove(Object owner) {
    if (owner == null) return;
    active.keySet().removeIf(operation -> operation.handlerOwner != null && operation.handlerOwner.get() == owner);
    entries.values().removeIf(entry -> {
      entry.owners.removeIf(reference -> reference.get() == owner);
      return entry.prune() && !active.containsValue(entry);
    });
  }
  private static String key(JmsOperation operation) {
    return String.valueOf(operation.row.get("destination")) + "\n" + operation.row.get("handler") + "\n" + operation.row.get("subscription") + "\n" + operation.row.get("handlerLoader");
  }
  private static final class Entry {
    final List<java.lang.ref.WeakReference<Object>> owners = new ArrayList<>();
    boolean owned;
    final String id = UUID.randomUUID().toString();
    final Map<String,Object> attributes = new LinkedHashMap<>();
    void observe(JmsOperation operation) {
      attributes.putAll(operation.row);
      register(operation.handlerOwner);
    }
    void register(java.lang.ref.WeakReference<Object> reference) {
      Object owner = reference == null ? null : reference.get();
      if (owner == null) return;
      owned = true;
      if (owners.stream().noneMatch(existing -> existing.get() == owner)) owners.add(reference);
    }
    boolean prune() {
      owners.removeIf(reference -> {
        Object owner = reference.get();
        return owner == null || com.flanksource.telltale.observer.ObserverRuntime.retiredTarget(owner.getClass());
      });
      return owned && owners.isEmpty();
    }
  }
}
