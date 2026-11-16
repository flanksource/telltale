package com.flanksource.telltale.observer.sql;

import java.lang.ref.WeakReference;
import java.lang.reflect.*;
import java.util.*;
import java.util.function.BiConsumer;
import java.util.function.Consumer;

final class R2dbcSupport {
  private final Map<ClassLoader, WeakReference<Class<?>>> bridges = new WeakHashMap<>();
  private final Map<String, byte[]> bytecode = SqlHelperBytecode.load(R2dbcSupport.class.getClassLoader());

  Object observe(Object statement, Object publisher, Map<String, Object> metadata,
      Consumer<Map<String, Object>> started, Consumer<Map<String, Object>> changed,
      BiConsumer<Map<String, Object>, Throwable> ended, Consumer<Throwable> failed) {
    try {
      Class<?> bridge = bridge(statement.getClass().getClassLoader());
      return bridge.getMethod("observe", Object.class, Object.class, Map.class, Consumer.class, Consumer.class, BiConsumer.class, Consumer.class)
          .invoke(null, statement, publisher, metadata, started, changed, ended, failed);
    } catch (InvocationTargetException error) {
      throw new IllegalStateException("observe R2DBC execution", error.getCause());
    } catch (ReflectiveOperationException error) {
      throw new IllegalStateException("load R2DBC execution observer", error);
    }
  }

  private synchronized Class<?> bridge(ClassLoader target) throws ClassNotFoundException {
    WeakReference<Class<?>> reference = bridges.get(target);
    Class<?> bridge = reference == null ? null : reference.get();
    if (bridge == null) {
      if (!bridges.containsKey(target) && bridges.size() >= 256) throw new IllegalStateException("SQL target classloader limit exceeded: 256");
      try { Class.forName("reactor.util.context.ContextView", false, target); }
      catch (ClassNotFoundException error) {
        throw new ClassNotFoundException("R2DBC observation requires compatible Reactor classes in the target classloader", error);
      }
      bridge = Class.forName(R2dbcSupport.class.getPackage().getName() + ".R2dbcBridge", true, new BridgeLoader(target, bytecode));
      bridges.put(target, new WeakReference<>(bridge));
    }
    return bridge;
  }

  private static final class BridgeLoader extends ClassLoader {
    final Map<String, byte[]> bytecode;
    BridgeLoader(ClassLoader target, Map<String, byte[]> bytecode) { super(target); this.bytecode = bytecode; }

    protected synchronized Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!name.startsWith(R2dbcSupport.class.getPackage().getName() + ".R2dbcBridge") && !name.startsWith("io.r2dbc.proxy."))
        return super.loadClass(name, resolve);
      Class<?> loaded = findLoadedClass(name);
      if (loaded == null) loaded = define(name);
      if (resolve) resolveClass(loaded);
      return loaded;
    }

    private Class<?> define(String name) throws ClassNotFoundException {
      byte[] content = bytecode.get(name);
      if (content == null) throw new ClassNotFoundException("R2DBC helper bytecode is not cached: " + name);
      return defineClass(name, content, 0, content.length);
    }
  }
}
