package com.flanksource.telltale.observer;

import java.lang.ref.*;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.*;
import com.flanksource.telltale.diag.hotpatch.ClassArtifact;
import org.objectweb.asm.ClassReader;

/** Classfile summaries contain no Class or strong ClassLoader references. */
final class CallbackIndex {
  private static final int MAX_CLASSES = 65536;
  private final ReferenceQueue<ClassLoader> released = new ReferenceQueue<>();
  private final Map<Key, Boolean> classes = new LinkedHashMap<Key, Boolean>(1024, .75f, true) {
    protected boolean removeEldestEntry(Map.Entry<Key, Boolean> entry) { return size() > MAX_CLASSES; }
  };

  synchronized Boolean known(ClassLoader loader, String name) {
    purge(); return classes.get(new Key(loader, name, null));
  }

  Boolean known(Class<?> type) {
    Boolean known = known(type.getClassLoader(), type.getName());
    if (known != null) return known;
    try (InputStream input = ClassArtifact.open(type); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
      if (input == null) return null;
      byte[] buffer = new byte[4096];
      for (int count; (count = input.read(buffer)) != -1;) output.write(buffer, 0, count);
      byte[] bytes = output.toByteArray();
      if (!new ClassReader(bytes).getClassName().equals(type.getName().replace('.', '/'))) {
        throw new IllegalArgumentException("code-source artifact names a different class");
      }
      return remember(type.getClassLoader(), type.getName(), bytes);
    } catch (IOException | SecurityException | IllegalArgumentException error) {
      throw new IllegalStateException("cannot inspect callback artifact for " + type.getName(), error);
    }
  }

  boolean remember(ClassLoader loader, String name, byte[] bytes) {
    String pool = new String(bytes, StandardCharsets.ISO_8859_1);
    boolean callback = pool.contains("/jms/MessageListener") || pool.contains("/jms/CompletionListener");
    synchronized (this) { purge(); classes.put(new Key(loader, name, released), callback); }
    return callback;
  }

  private void purge() {
    for (Reference<?> dead; (dead = released.poll()) != null;) classes.remove(dead);
  }

  private static final class Key extends WeakReference<ClassLoader> {
    final String name;
    final int hash;
    final boolean bootstrap;
    Key(ClassLoader loader, String name, ReferenceQueue<ClassLoader> queue) {
      super(loader, queue); this.name = name; bootstrap = loader == null;
      hash = 31 * System.identityHashCode(loader) + name.hashCode();
    }
    public int hashCode() { return hash; }
    public boolean equals(Object other) {
      if (this == other) return true;
      if (!(other instanceof Key)) return false;
      Key key = (Key) other;
      return hash == key.hash && name.equals(key.name) && bootstrap == key.bootstrap
          && (bootstrap || get() != null && get() == key.get());
    }
  }
}
