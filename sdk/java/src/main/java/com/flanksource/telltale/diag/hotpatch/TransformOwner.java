package com.flanksource.telltale.diag.hotpatch;

import java.lang.reflect.InvocationTargetException;

final class TransformOwner {
  private TransformOwner() {}

  static boolean owns(Class<?> local) { return owner(local) == local; }

  static Object invoke(Class<?> local, String operation, Object[] args) {
    try { return owner(local).getMethod("dispatch", String.class, Object[].class).invoke(null, operation, args); }
    catch (InvocationTargetException error) {
      Throwable cause = error.getCause();
      if (cause instanceof RuntimeException) throw (RuntimeException) cause;
      if (cause instanceof Error) throw (Error) cause;
      throw new IllegalStateException("JVM transform owner failed: " + operation, cause);
    } catch (ReflectiveOperationException error) { throw new IllegalStateException("incompatible JVM transform owner: " + operation, error); }
  }

  private static Class<?> owner(Class<?> local) {
    try { return Class.forName(local.getName(), false, com.flanksource.telltale.RuntimeOwner.loader()); }
    catch (ClassNotFoundException error) {
      throw new IllegalStateException("the attached agent has no JVM transform coordinator " + local.getName(), error);
    }
  }
}
