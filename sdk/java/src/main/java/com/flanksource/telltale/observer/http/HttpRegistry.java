package com.flanksource.telltale.observer.http;

import java.lang.ref.ReferenceQueue;
import java.lang.ref.WeakReference;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;
import java.util.function.IntSupplier;

final class HttpRegistry {
  final Map<String, HttpOperation> active = new ConcurrentHashMap<>();
  private final Map<Identity, Binding> bindings = new ConcurrentHashMap<>();
  private final Map<HttpOperation, Set<Identity>> owners = new ConcurrentHashMap<>();
  private final Set<Identity> completedConnections = Collections.newSetFromMap(new ConcurrentHashMap<Identity, Boolean>());
  private final ReferenceQueue<Object> released = new ReferenceQueue<>();
  private final ThreadLocal<Deque<HttpOperation>> contexts = ThreadLocal.withInitial(ArrayDeque::new);
  private final BooleanSupplier enabled;
  private final IntSupplier bodyLimit;
  private final Consumer<Map<String, Object>> completed;

  HttpRegistry(BooleanSupplier enabled, IntSupplier bodyLimit, Consumer<Map<String, Object>> completed) {
    this.enabled = enabled; this.bodyLimit = bodyLimit; this.completed = completed;
  }

  HttpOperation begin(String direction, String method, String url, Object owner) {
    if (!enabled.getAsBoolean()) return null;
    Binding previous = binding(owner);
    if (previous == null && owner != null && HttpAccess.has(owner, "getNativeRequest")) previous = binding(HttpAccess.call(owner, "getNativeRequest"));
    if (previous != null && !previous.operation.finished()) { previous.operation.directObserved = true; return previous.operation; }
    HttpOperation operation = current();
    if (operation == null || !operation.direction.equals(direction) || operation.finished()) {
      operation = create(direction, method, url);
    }
    if (operation == null) return null;
    operation.directObserved = true;
    bind(owner, operation, false);
    return operation;
  }

  HttpOperation create(String direction, String method, String url) {
    if (!enabled.getAsBoolean()) return null;
    if (active.size() >= 10000) throw new IllegalStateException("HTTP active operation limit exceeded");
    HttpOperation operation = new HttpOperation(direction, method, url, bodyLimit.getAsInt(), completed);
    active.put(operation.id, operation);
    return operation;
  }

  HttpOperation current() { return contexts.get().peek(); }
  void push(HttpOperation operation) { contexts.get().push(operation); }
  void pop(HttpOperation operation) {
    Deque<HttpOperation> stack = contexts.get();
    if (stack.isEmpty() || stack.pop() != operation) throw new IllegalStateException("HTTP context stack is unbalanced");
    if (stack.isEmpty()) contexts.remove();
  }

  void finish(HttpOperation operation, Throwable error) {
    try { operation.finish(error); }
    finally { if (operation.finished()) remove(operation); }
  }

  private void remove(HttpOperation operation) {
    active.remove(operation.id, operation);
    Set<Identity> references = owners.remove(operation);
    if (references != null) for (Identity reference : references) {
      Binding binding = bindings.get(reference);
      if (reference.get() instanceof java.net.HttpURLConnection) completedConnections.add(reference);
      if (binding != null && binding.operation == operation) bindings.remove(reference, binding);
    }
  }

  void bind(Object owner, HttpOperation operation, boolean requestBody) {
    if (owner == null || operation == null) return;
    Identity old;
    while ((old = (Identity) released.poll()) != null) { bindings.remove(old); completedConnections.remove(old); }
    Identity identity = new Identity(owner, released);
    bindings.put(identity, new Binding(operation, requestBody));
    owners.computeIfAbsent(operation, ignored -> Collections.newSetFromMap(new ConcurrentHashMap<Identity, Boolean>())).add(identity);
  }

  Binding binding(Object owner) { return owner == null ? null : bindings.get(new Identity(owner, null)); }
  void unbind(Object owner) { if (owner != null) bindings.remove(new Identity(owner, null)); }
  boolean completedConnection(Object owner) { return completedConnections.contains(new Identity(owner, null)); }
  void resumeConnection(Object owner) { completedConnections.remove(new Identity(owner, null)); }

  List<Map<String, Object>> snapshot() {
    List<Map<String, Object>> rows = new ArrayList<>();
    for (HttpOperation operation : active.values()) if (!operation.finished()) rows.add(operation.snapshot());
    rows.sort((left, right) -> Long.compare(((Number) right.get("elapsedNs")).longValue(), ((Number) left.get("elapsedNs")).longValue()));
    return rows;
  }

  void clear() { for (HttpOperation operation : active.values()) operation.retire(); active.clear(); bindings.clear(); owners.clear(); completedConnections.clear(); }

  static final class Binding {
    final HttpOperation operation;
    final boolean request;
    Binding(HttpOperation operation, boolean request) { this.operation = operation; this.request = request; }
  }

  private static final class Identity extends WeakReference<Object> {
    final int hash;
    Identity(Object value, ReferenceQueue<Object> queue) { super(value, queue); hash = System.identityHashCode(value); }
    @Override public int hashCode() { return hash; }
    @Override public boolean equals(Object other) { return this == other || other instanceof Identity && get() != null && get() == ((Identity) other).get(); }
  }
}
