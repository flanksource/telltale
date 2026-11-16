package com.flanksource.telltale.observer.http;

import java.lang.reflect.*;
import java.util.*;

final class HttpAccess {
  private HttpAccess() {}

  static Object call(Object target, String name, Object... args) {
    if (target == null) throw new IllegalArgumentException("HTTP metadata target is null for " + name);
    Method method = method(target.getClass(), name, args);
    if (method == null) throw new IllegalArgumentException("HTTP metadata method " + target.getClass().getName() + "." + name + " was not found");
    try { return method.invoke(target, args); }
    catch (InvocationTargetException error) { throw new IllegalStateException("HTTP metadata " + name + " failed", error.getCause()); }
    catch (ReflectiveOperationException error) { throw new IllegalStateException("HTTP metadata " + name + " is inaccessible", error); }
  }

  static boolean has(Object target, String name, Object... args) { return target != null && method(target.getClass(), name, args) != null; }

  static Object callStatic(ClassLoader loader, String typeName, String name, Object... args) {
    try {
      Class<?> type = Class.forName(typeName, false, loader);
      Method method = method(type, name, args);
      if (method == null) throw new IllegalArgumentException("HTTP publisher factory is missing: " + typeName + "." + name);
      return method.invoke(null, args);
    } catch (InvocationTargetException error) { throw new IllegalStateException("HTTP publisher factory failed: " + name, error.getCause()); }
    catch (ReflectiveOperationException error) { throw new IllegalStateException("HTTP publisher API is unavailable: " + typeName, error); }
  }

  private static Method method(Class<?> type, String name, Object[] args) {
    if (type == null) return null;
    for (Class<?> contract : type.getInterfaces()) { Method method = method(contract, name, args); if (method != null) return method; }
    Method inherited = method(type.getSuperclass(), name, args);
    if (inherited != null) return inherited;
    if (Modifier.isPublic(type.getModifiers())) for (Method method : type.getDeclaredMethods()) {
      if (!Modifier.isPublic(method.getModifiers()) || !method.getName().equals(name) || method.getParameterTypes().length != args.length) continue;
      boolean accepts = true;
      for (int i = 0; i < args.length; i++) if (args[i] != null && !boxed(method.getParameterTypes()[i]).isInstance(args[i])) accepts = false;
      if (accepts) return method;
    }
    return null;
  }

  private static Class<?> boxed(Class<?> type) {
    if (type == int.class) return Integer.class;
    if (type == long.class) return Long.class;
    if (type == boolean.class) return Boolean.class;
    return type;
  }

  static String text(Object target, String name) { Object value = call(target, name); return value == null ? "" : value.toString(); }

  @SuppressWarnings("unchecked")
  static Map<String, List<String>> headers(Object value) {
    Map<String, List<String>> result = new LinkedHashMap<>();
    if (value instanceof Map) {
      for (Map.Entry<?, ?> entry : ((Map<?, ?>) value).entrySet()) {
        if (entry.getKey() == null) continue;
        Object values = entry.getValue();
        List<String> items = new ArrayList<>();
        if (values instanceof Iterable) for (Object item : (Iterable<?>) values) items.add(String.valueOf(item));
        else if (values != null) items.add(values.toString());
        result.put(entry.getKey().toString(), items);
      }
    } else if (has(value, "asMultiValueMap")) return headers(call(value, "asMultiValueMap"));
    else if (has(value, "map")) return headers(call(value, "map"));
    else if (has(value, "toMultimap")) return headers(call(value, "toMultimap"));
    else throw new IllegalArgumentException("unsupported HTTP headers " + (value == null ? "null" : value.getClass().getName()));
    return result;
  }

  static Map<String, List<String>> servletHeaders(Object message, boolean request) {
    Map<String, List<String>> headers = new LinkedHashMap<>();
    Object names = call(message, "getHeaderNames");
    Iterable<?> iterable = names instanceof Enumeration ? Collections.list((Enumeration<?>) names) : (Iterable<?>) names;
    for (Object name : iterable) {
      Object values = call(message, "getHeaders", name.toString());
      List<String> items = new ArrayList<>();
      if (values instanceof Enumeration) for (Object value : Collections.list((Enumeration<?>) values)) items.add(value.toString());
      else for (Object value : (Iterable<?>) values) items.add(value.toString());
      headers.put(name.toString(), items);
    }
    return headers;
  }

  static Map<String, List<String>> apacheHeaders(Object message) {
    Map<String, List<String>> headers = new LinkedHashMap<>();
    Object values = HttpAccess.has(message, "getAllHeaders") ? call(message, "getAllHeaders") : call(message, "getHeaders");
    for (int i = 0; i < Array.getLength(values); i++) {
      Object header = Array.get(values, i);
      headers.computeIfAbsent(text(header, "getName"), ignored -> new ArrayList<>()).add(text(header, "getValue"));
    }
    return headers;
  }
}
