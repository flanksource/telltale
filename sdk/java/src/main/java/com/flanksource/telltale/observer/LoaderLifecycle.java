package com.flanksource.telltale.observer;

import java.util.*;

/** A retired application loader forbids verification and is no longer a live capture target. */
final class LoaderLifecycle {
  private LoaderLifecycle() {}

  static Map<String, Object> retired(ClassLoader loader) {
    if (loader == null) return null;
    Class<?> contract = loader.getClass();
    String target = OtelTargets.name("loader.tomcat.type");
    while (contract != null && !contract.getName().equals(target)) contract = contract.getSuperclass();
    if (contract == null) return null;
    try {
      Object value = contract.getMethod("getState").invoke(loader);
      if (!(value instanceof Enum)) throw new IllegalStateException("Tomcat loader lifecycle state is not an enum: " + value);
      String state = ((Enum<?>) value).name();
      if (!Arrays.asList(OtelTargets.name("loader.tomcat.retiredStates").split(",")).contains(state)) return null;
      Map<String, Object> retired = new LinkedHashMap<>();
      retired.put("id", Integer.toHexString(System.identityHashCode(loader)));
      retired.put("class", loader.getClass().getName());
      retired.put("state", state);
      return retired;
    } catch (ReflectiveOperationException error) {
      throw new IllegalStateException("read application loader lifecycle " + loader.getClass().getName(), error);
    }
  }

  static void requireActive(Class<?> type) {
    Map<String, Object> retired = retired(type.getClassLoader());
    if (retired != null) throw new IllegalStateException("cannot instrument retired application class "
        + type.getName() + ": loader " + retired.get("id") + " is " + retired.get("state"));
  }
}
