package com.flanksource.telltale.observer.jms;

import java.lang.ref.*;
import java.util.*;

final class JmsPending {
  private final ReferenceQueue<Object> collected = new ReferenceQueue<>();
  private final Map<Key, JmsOperation> values = new HashMap<>();
  private final int maximum;

  JmsPending(int maximum) { this.maximum = maximum; }

  synchronized boolean put(Object message, JmsOperation operation) {
    Key key = new Key(message, collected);
    if (values.size() >= maximum && !values.containsKey(key)) return false;
    values.put(key, operation);
    return true;
  }

  synchronized JmsOperation remove(Object message) { return values.remove(new Key(message, null)); }

  synchronized List<JmsOperation> collected() {
    List<JmsOperation> lost = new ArrayList<>();
    for (Reference<?> key; (key = collected.poll()) != null;) {
      JmsOperation operation = values.remove(key);
      if (operation != null) lost.add(operation);
    }
    return lost;
  }

  synchronized List<JmsOperation> drain() {
    List<JmsOperation> unfinished = new ArrayList<>(values.values());
    values.clear();
    while (collected.poll() != null) {}
    return unfinished;
  }

  private static final class Key extends WeakReference<Object> {
    private final int identity;
    Key(Object message, ReferenceQueue<Object> queue) { super(message, queue); identity = System.identityHashCode(message); }
    public int hashCode() { return identity; }
    public boolean equals(Object other) {
      if (this == other) return true;
      Object value = get();
      return other instanceof Key && value != null && value == ((Key) other).get();
    }
  }
}
