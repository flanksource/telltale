package com.flanksource.telltale.diag.jmx;

import java.lang.management.ManagementFactory;
import java.lang.reflect.Array;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import javax.management.Attribute;
import javax.management.AttributeList;
import javax.management.MBeanAttributeInfo;
import javax.management.MBeanInfo;
import javax.management.MBeanNotificationInfo;
import javax.management.MBeanOperationInfo;
import javax.management.MBeanParameterInfo;
import javax.management.MBeanServer;
import javax.management.ObjectInstance;
import javax.management.ObjectName;
import javax.management.openmbean.CompositeData;
import javax.management.openmbean.CompositeType;
import javax.management.openmbean.TabularData;

/**
 * Generic JMX access over the process-wide platform MBeanServer — the same one application
 * registers its MBeans on. Everything is keyed by {@link ObjectName}, so no application classes are
 * needed. Open types (CompositeData/TabularData) are lowered to plain maps/lists for JSON.
 */
public final class JmxService {

  private final MBeanServer mbs = ManagementFactory.getPlatformMBeanServer();

  public MBeanServer server() {
    return mbs;
  }

  /** queryNames matching a pattern (null/"" =&gt; all). */
  public List<String> search(String pattern) throws Exception {
    ObjectName query = (pattern == null || pattern.isEmpty()) ? null : new ObjectName(pattern);
    Set<ObjectInstance> instances = mbs.queryMBeans(query, null);
    List<String> names = new ArrayList<>(instances.size());
    for (ObjectInstance oi : instances) {
      names.add(oi.getObjectName().getCanonicalName());
    }
    return names;
  }

  public boolean isRegistered(String objectName) throws Exception {
    return mbs.isRegistered(new ObjectName(objectName));
  }

  /** MBeanInfo lowered to a JSON-friendly map with complete operator metadata. */
  public Map<String, Object> info(String objectName) throws Exception {
    ObjectName on = new ObjectName(objectName);
    MBeanInfo mi = mbs.getMBeanInfo(on);
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("objectName", on.getCanonicalName());
    out.put("className", mi.getClassName());
    out.put("description", mi.getDescription());
    List<Map<String, Object>> attrs = new ArrayList<>();
    for (MBeanAttributeInfo a : mi.getAttributes()) {
      Map<String, Object> attr = new LinkedHashMap<>();
      attr.put("name", a.getName());
      attr.put("description", a.getDescription());
      attr.put("type", a.getType());
      attr.put("readable", a.isReadable());
      attr.put("writable", a.isWritable());
      attr.put("is", a.isIs());
      attrs.add(attr);
    }
    out.put("attributes", attrs);
    List<Map<String, Object>> ops = new ArrayList<>();
    for (MBeanOperationInfo o : mi.getOperations()) {
      Map<String, Object> operation = new LinkedHashMap<>();
      operation.put("name", o.getName());
      operation.put("description", o.getDescription());
      operation.put("returnType", o.getReturnType());
      operation.put("impact", o.getImpact());
      List<Map<String, Object>> signature = new ArrayList<>();
      for (MBeanParameterInfo parameter : o.getSignature()) {
        Map<String, Object> item = new LinkedHashMap<>();
        item.put("name", parameter.getName());
        item.put("description", parameter.getDescription());
        item.put("type", parameter.getType());
        signature.add(item);
      }
      operation.put("signature", signature);
      ops.add(operation);
    }
    out.put("operations", ops);
    List<Map<String, Object>> notifications = new ArrayList<>();
    for (MBeanNotificationInfo n : mi.getNotifications()) {
      Map<String, Object> notification = new LinkedHashMap<>();
      notification.put("name", n.getName());
      notification.put("description", n.getDescription());
      notification.put("types", java.util.Arrays.asList(n.getNotifTypes()));
      notifications.add(notification);
    }
    out.put("notifications", notifications);
    return out;
  }

  public Object read(String objectName, String attribute) throws Exception {
    return simplify(mbs.getAttribute(new ObjectName(objectName), attribute));
  }

  public Map<String, Object> readAll(String objectName) throws Exception {
    ObjectName on = new ObjectName(objectName);
    MBeanInfo info = mbs.getMBeanInfo(on);
    List<String> names = new ArrayList<>();
    for (MBeanAttributeInfo attribute : info.getAttributes()) {
      if (attribute.isReadable()) {
        names.add(attribute.getName());
      }
    }
    AttributeList values = mbs.getAttributes(on, names.toArray(new String[0]));
    Map<String, Object> byName = new LinkedHashMap<>();
    for (Object value : values) {
      Attribute attribute = (Attribute) value;
      byName.put(attribute.getName(), simplify(attribute.getValue()));
    }
    List<Map<String, Object>> attributes = new ArrayList<>();
    for (String name : names) {
      if (!byName.containsKey(name)) {
        continue;
      }
      Map<String, Object> attribute = new LinkedHashMap<>();
      attribute.put("name", name);
      attribute.put("value", byName.get(name));
      attributes.add(attribute);
    }
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("objectName", on.getCanonicalName());
    out.put("attributes", attributes);
    return out;
  }

  public void write(String objectName, String attribute, Object value) throws Exception {
    ObjectName on = new ObjectName(objectName);
    String type = attributeType(mbs.getMBeanInfo(on), attribute);
    mbs.setAttribute(on, new Attribute(attribute, convert(value, type)));
  }

  public Object invoke(String objectName, String operation, Object[] args, String[] signature)
      throws Exception {
    if (args.length != signature.length) {
      throw new IllegalArgumentException("arguments and signature must have the same length");
    }
    Object[] converted = new Object[args.length];
    for (int i = 0; i < args.length; i++) {
      converted[i] = convert(args[i], signature[i]);
    }
    return simplify(mbs.invoke(new ObjectName(objectName), operation, converted, signature));
  }

  private static String attributeType(MBeanInfo info, String name) {
    for (MBeanAttributeInfo attribute : info.getAttributes()) {
      if (attribute.getName().equals(name)) {
        if (!attribute.isWritable()) {
          throw new IllegalArgumentException("MBean attribute is not writable: " + name);
        }
        return attribute.getType();
      }
    }
    throw new IllegalArgumentException("unknown MBean attribute: " + name);
  }

  @SuppressWarnings({"unchecked", "rawtypes"})
  static Object convert(Object value, String typeName) throws Exception {
    if (value == null) {
      if (isPrimitive(typeName)) {
        throw new IllegalArgumentException("null is invalid for primitive type " + typeName);
      }
      return null;
    }
    Class<?> type = resolveType(typeName);
    if (type.isInstance(value)) {
      return value;
    }
    String text = String.valueOf(value);
    if (type == String.class) return text;
    if (type == Boolean.class || type == boolean.class) {
      if (!"true".equalsIgnoreCase(text) && !"false".equalsIgnoreCase(text))
        throw new IllegalArgumentException("expected true or false for " + typeName);
      return Boolean.valueOf(text);
    }
    if (type == Byte.class || type == byte.class) return Byte.valueOf(text);
    if (type == Short.class || type == short.class) return Short.valueOf(text);
    if (type == Integer.class || type == int.class) return Integer.valueOf(text);
    if (type == Long.class || type == long.class) return Long.valueOf(text);
    if (type == Float.class || type == float.class) return Float.valueOf(text);
    if (type == Double.class || type == double.class) return Double.valueOf(text);
    if (type == Character.class || type == char.class) {
      if (text.length() != 1) throw new IllegalArgumentException("expected one character");
      return text.charAt(0);
    }
    if (type == ObjectName.class) return new ObjectName(text);
    if (type.isEnum()) return Enum.valueOf((Class<? extends Enum>) type, text);
    throw new IllegalArgumentException("unsupported JMX value type: " + typeName);
  }

  private static boolean isPrimitive(String typeName) {
    return "boolean".equals(typeName)
        || "byte".equals(typeName)
        || "short".equals(typeName)
        || "int".equals(typeName)
        || "long".equals(typeName)
        || "float".equals(typeName)
        || "double".equals(typeName)
        || "char".equals(typeName);
  }

  private static Class<?> resolveType(String typeName) throws ClassNotFoundException {
    if (isPrimitive(typeName)) {
      if ("boolean".equals(typeName)) return boolean.class;
      if ("byte".equals(typeName)) return byte.class;
      if ("short".equals(typeName)) return short.class;
      if ("int".equals(typeName)) return int.class;
      if ("long".equals(typeName)) return long.class;
      if ("float".equals(typeName)) return float.class;
      if ("double".equals(typeName)) return double.class;
      return char.class;
    }
    ClassLoader loader = Thread.currentThread().getContextClassLoader();
    return Class.forName(
        typeName, true, loader == null ? JmxService.class.getClassLoader() : loader);
  }

  /** Lower JMX open types into plain maps/lists so Jackson renders them cleanly. */
  public static Object simplify(Object value) {
    if (value == null) {
      return null;
    }
    if (value instanceof CompositeData) {
      CompositeData cd = (CompositeData) value;
      CompositeType ct = cd.getCompositeType();
      Map<String, Object> m = new LinkedHashMap<>();
      for (String key : ct.keySet()) {
        m.put(key, simplify(cd.get(key)));
      }
      return m;
    }
    if (value instanceof TabularData) {
      List<Object> rows = new ArrayList<>();
      for (Object row : ((TabularData) value).values()) {
        rows.add(simplify(row));
      }
      return rows;
    }
    if (value instanceof Object[]) {
      List<Object> list = new ArrayList<>();
      for (Object item : (Object[]) value) {
        list.add(simplify(item));
      }
      return list;
    }
    if (value instanceof Map) {
      Map<String, Object> out = new LinkedHashMap<>();
      for (Map.Entry<?, ?> entry : ((Map<?, ?>) value).entrySet()) {
        out.put(String.valueOf(entry.getKey()), simplify(entry.getValue()));
      }
      return out;
    }
    if (value instanceof Iterable) {
      List<Object> out = new ArrayList<>();
      for (Object item : (Iterable<?>) value) {
        out.add(simplify(item));
      }
      return out;
    }
    if (value.getClass().isArray()) {
      List<Object> out = new ArrayList<>();
      for (int i = 0; i < Array.getLength(value); i++) {
        out.add(simplify(Array.get(value, i)));
      }
      return out;
    }
    if (value instanceof Enum) {
      return ((Enum<?>) value).name();
    }
    if (value instanceof String || value instanceof Number || value instanceof Boolean) {
      return value;
    }
    // Fall back to a string form for application-specific / non-open types we can't
    // introspect without their classes.
    return String.valueOf(value);
  }
}
