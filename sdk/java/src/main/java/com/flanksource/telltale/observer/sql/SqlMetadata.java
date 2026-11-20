package com.flanksource.telltale.observer.sql;

import java.lang.ref.ReferenceQueue;
import java.lang.ref.WeakReference;
import java.util.*;

final class SqlMetadata {
  private final ReferenceQueue<Object> released = new ReferenceQueue<>();
  private final Map<Key, Map<String, Object>> values = new HashMap<>();
  private final int limit;

  SqlMetadata(int limit) { this.limit = limit; }

  synchronized Map<String, Object> get(Object object) {
    expunge();
    Map<String, Object> value = values.get(new Key(object, null));
    return value == null ? null : new LinkedHashMap<>(value);
  }

  synchronized void put(Object object, Map<String, Object> metadata) {
    expunge();
    if (!values.containsKey(new Key(object, null)) && values.size() >= limit)
      throw new IllegalStateException("SQL metadata limit exceeded: " + limit);
    values.put(new Key(object, released), new LinkedHashMap<>(metadata));
  }

  synchronized void remove(Object object) {
    expunge();
    values.remove(new Key(object, null));
  }

  private void expunge() {
    Object key;
    while ((key = released.poll()) != null) values.remove(key);
  }

  private static final class Key extends WeakReference<Object> {
    final int hash;
    Key(Object value, ReferenceQueue<Object> queue) {
      super(value, queue);
      hash = System.identityHashCode(value);
    }
    public int hashCode() { return hash; }
    public boolean equals(Object other) {
      if (this == other) return true;
      return other instanceof Key && get() != null && get() == ((Key) other).get();
    }
  }
}
