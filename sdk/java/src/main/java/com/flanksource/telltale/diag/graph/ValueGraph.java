package com.flanksource.telltale.diag.graph;

import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Modifier;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.Date;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;

/**
 * Walks a live Java value into the object tree the diagnostics UI renders.
 *
 * <p>One source, compiled twice. The WAR walks an OGNL expression's result with it; the agent jar
 * walks the arguments and return value of an instrumented method with it, relocated there to {@code
 * com.flanksource.telltale.diag.agent.graph} by the {@code agentJar} task so the copy on the system
 * class path can never shadow the webapp's. The two callers want different things from the same
 * walk, and {@link GraphLimits} is the whole difference:
 *
 * <ul>
 *   <li>{@link GraphLimits#ognl} — an operator asked for this value, on a servlet thread, and can
 *       expand any part of it later. Every node carries its OGNL path, what was not expanded says
 *       so, and any map or iterable is walked, exactly as the OGNL surface always has.
 *   <li>{@link GraphLimits#probe} — nobody asked application's thread to render anything, so the
 *       walk runs <em>no application code</em> and takes no monitor. It reads fields instead of
 *       calling getters, calls {@code toString()} only on the JDK value types {@link InertTypes}
 *       names, and iterates only the JDK collections it names. A Hibernate collection is shown by
 *       its fields — its backing list once initialised, {@code initialized=false} before — and is
 *       never loaded. Depth, children per container, nodes and characters are all bounded, because
 *       the tree is held in the agent's ring until someone reads it.
 * </ul>
 *
 * <p>No static state, deliberately: in the agent the first walk can run on an application thread,
 * where a failing {@code <clinit>} would be an {@code ExceptionInInitializerError} in application's
 * own call path for the life of the JVM.
 */
public final class ValueGraph {

  /** Shown in place of a probe's bean past the depth limit, which nothing can expand later. */
  private static final String NOT_EXPANDED = "not expanded: past the expand depth";

  private final GraphLimits limits;

  /**
   * Objects already walked, with where they were first shown.
   *
   * <p>In a probe snapshot this spans the whole event, so an object reached twice — a cycle, or one
   * DCL passed as two arguments — is shown once and pointed at after that. The OGNL surface keeps
   * only the ancestors of the node being built, as it always has: a shared reference is shown in
   * full wherever it appears and only a cycle is cut.
   */
  private final IdentityHashMap<Object, String> seen = new IdentityHashMap<Object, String>();

  private int nodes;
  private long chars;
  private long bytes;
  private boolean truncated;

  public ValueGraph(GraphLimits limits) {
    this.limits = limits;
  }

  /**
   * Walks {@code value} into a tree whose root is labelled {@code label}.
   *
   * @param path the root's OGNL accessor, or null for a probe snapshot — which then carries no ids
   *     and no paths, since the object is gone by the time anyone could expand it and the lowering
   *     assigns ids itself
   */
  public Node root(Object value, String label, String path) {
    return node(value, limits.depth, path == null ? null : "$", label, path, null);
  }

  /** Roughly what this walk retained, in bytes, for the agent's memory budget. */
  public long bytes() {
    return bytes;
  }

  /** Whether any bound cut something: a string, a container, the depth, or the whole walk. */
  public boolean truncated() {
    return truncated;
  }

  /** A value recorded as its class and identity only, reading nothing from it. */
  public static Node opaque(String label, Object value) {
    return new Node(
        null,
        label,
        "opaque",
        null,
        false,
        value.getClass().getName(),
        null,
        identity(value),
        null);
  }

  /** A root that stands for values not recorded, e.g. arguments past the argument cap. */
  public static Node marker(String label, String value) {
    return new Node(null, label, "scalar", null, false, null, value, null, null);
  }

  private Node node(
      Object value, int depth, String id, String label, String path, String parentTrail) {
    nodes++;
    if (value == null) {
      return scalar(id, label, path, "null", null);
    }
    Class<?> type = value.getClass();
    if (isScalar(type, value)) {
      return scalar(id, label, path, scalarText(value), type.getName());
    }
    String seenAt = seen.get(value);
    if (seenAt != null) {
      return limits.inert
          ? leaf(
              id, label, path, "identity", type.getName(), text("same object as " + seenAt), false)
          : leaf(id, label, path, "identity", type.getName(), identity(value), true);
    }
    // Remembered only where it is expanded: past the depth none of it is shown, so a later,
    // shallower occurrence
    // pointing back there would lose the object from the capture entirely.
    boolean expands = depth > 0;
    if (expands) {
      seen.put(value, limits.inert ? trail(parentTrail, label) : "");
    }
    try {
      if (type.isArray()) {
        return listNode(value, type, Array.getLength(value), depth, id, label, path, parentTrail);
      }
      if (value instanceof Map && iterates(type)) {
        return mapNode((Map<?, ?>) value, depth, id, label, path, parentTrail);
      }
      if (value instanceof Iterable && iterates(type)) {
        return listNode(value, type, size(value), depth, id, label, path, parentTrail);
      }
      return beanNode(value, depth, id, label, path, parentTrail);
    } finally {
      if (expands && !limits.inert) {
        seen.remove(value);
      }
    }
  }

  private Node mapNode(
      Map<?, ?> map, int depth, String id, String label, String path, String parentTrail) {
    String type = map.getClass().getName();
    int size = map.size();
    if (depth == 0) {
      cutAtDepth(size > 0);
      return leaf(id, label, path, "map", type, "size=" + size, !limits.inert);
    }
    String trail = trail(parentTrail, label);
    List<Node> children = new ArrayList<Node>(Math.min(size, limits.children));
    int index = 0;
    try {
      for (Map.Entry<?, ?> entry : map.entrySet()) {
        if (index >= limits.children || spent()) {
          break;
        }
        children.add(entry(entry, depth, id, path, trail, index));
        index++;
      }
    } catch (RuntimeException e) {
      if (!limits.inert) {
        throw e;
      }
      children.add(failure(id, path, "iteration", e));
    }
    appendTruncation(children, size, index, id, path);
    return branch(id, label, path, "map", type, children);
  }

  /**
   * One map entry. The OGNL surface labels it with the key's text, as it always has; a probe does
   * that only for a key it may render, and otherwise shows the key and the value side by side under
   * the entry's position.
   */
  private Node entry(
      Map.Entry<?, ?> entry, int depth, String id, String path, String trail, int index) {
    Object key = entry.getKey();
    if (!limits.inert) {
      String text = String.valueOf(key);
      return node(
          entry.getValue(),
          depth - 1,
          id + "." + index,
          text,
          path + ".get(\"" + escape(text) + "\")",
          trail);
    }
    if (key == null || isScalar(key.getClass(), key)) {
      String text = key == null ? "null" : scalarText(key);
      return node(entry.getValue(), depth - 1, null, text, null, trail);
    }
    String position = "#" + index;
    List<Node> pair = new ArrayList<Node>(2);
    pair.add(node(key, depth - 1, null, "key", null, trail(trail, position)));
    pair.add(node(entry.getValue(), depth - 1, null, "value", null, trail(trail, position)));
    return branch(null, position, null, "bean", "java.util.Map$Entry", pair);
  }

  private Node listNode(
      Object value,
      Class<?> type,
      int size,
      int depth,
      String id,
      String label,
      String path,
      String parentTrail) {
    String listLabel = label + " [" + size + "]";
    if (depth == 0) {
      cutAtDepth(size > 0);
      return leaf(id, listLabel, path, "list", type.getName(), "size=" + size, !limits.inert);
    }
    String trail = trail(parentTrail, label);
    List<Node> children = new ArrayList<Node>(Math.min(size, limits.children));
    int index = 0;
    try {
      if (type.isArray()) {
        for (; index < size && index < limits.children && !spent(); index++) {
          children.add(element(Array.get(value, index), depth, id, path, trail, index));
        }
      } else {
        for (Object item : (Iterable<?>) value) {
          if (index >= limits.children || spent()) {
            break;
          }
          children.add(element(item, depth, id, path, trail, index));
          index++;
        }
      }
    } catch (RuntimeException e) {
      if (!limits.inert) {
        throw e;
      }
      children.add(failure(id, path, "iteration", e));
    }
    appendTruncation(children, size, index, id, path);
    return branch(id, listLabel, path, "list", type.getName(), children);
  }

  private Node element(Object item, int depth, String id, String path, String trail, int index) {
    String position = String.valueOf(index);
    return node(
        item,
        depth - 1,
        id == null ? null : id + "." + position,
        position,
        path == null ? null : path + "[" + index + "]",
        trail);
  }

  private Node beanNode(
      Object value, int depth, String id, String label, String path, String parentTrail) {
    Class<?> type = value.getClass();
    if (depth == 0) {
      cutAtDepth(true);
      return limits.inert
          ? leaf(id, label, path, "opaque", type.getName(), NOT_EXPANDED, false)
          : leaf(id, label, path, "opaque", type.getName(), identity(value), true);
    }
    List<Field> fields;
    try {
      fields = fields(type);
    } catch (RuntimeException | LinkageError e) {
      // A field whose type cannot be resolved (NoClassDefFoundError), or a SecurityManager refusing
      // the read.
      if (!limits.inert) {
        throw e;
      }
      truncated = true;
      return leaf(
          id,
          label,
          path,
          "error",
          type.getName(),
          "fields unreadable: " + e.getClass().getName(),
          false);
    }
    String trail = trail(parentTrail, label);
    List<Node> children = new ArrayList<Node>(Math.min(fields.size(), limits.children));
    int count = 0;
    for (Field field : fields) {
      if (count >= limits.children || spent()) {
        break;
      }
      children.add(field(value, field, depth, id, path, trail));
      count++;
    }
    appendTruncation(children, fields.size(), count, id, path);
    return branch(id, label, path, "bean", type.getName(), children);
  }

  private Node field(Object owner, Field field, int depth, String id, String path, String trail) {
    String name = field.getName();
    String childId = id == null ? null : id + "." + name;
    String childPath = path == null ? null : path + "." + name;
    try {
      if (!field.isAccessible()) {
        field.setAccessible(true);
      }
      return node(field.get(owner), depth - 1, childId, name, childPath, trail);
    } catch (RuntimeException | IllegalAccessException e) {
      if (!limits.inert) {
        return leaf(
            childId, name, childPath, "opaque", field.getType().getName(), e.getMessage(), false);
      }
      truncated = true;
      return leaf(
          childId,
          name,
          childPath,
          "error",
          field.getType().getName(),
          "unreadable: " + e.getClass().getName(),
          false);
    }
  }

  private static List<Field> fields(Class<?> type) {
    List<Field> fields = new ArrayList<Field>();
    for (Class<?> current = type;
        current != null && current != Object.class;
        current = current.getSuperclass()) {
      for (Field field : current.getDeclaredFields()) {
        if (Modifier.isStatic(field.getModifiers())
            || field.isSynthetic()
            || field.getName().startsWith("CGLIB$")) {
          continue;
        }
        fields.add(field);
      }
    }
    Collections.sort(
        fields,
        new Comparator<Field>() {
          @Override
          public int compare(Field a, Field b) {
            return a.getName().compareTo(b.getName());
          }
        });
    return fields;
  }

  /** Whether a value renders as one line of text. For a probe, see {@link InertTypes#scalar}. */
  private boolean isScalar(Class<?> type, Object value) {
    if (limits.inert) {
      return InertTypes.scalar(type, value);
    }
    return type.isPrimitive()
        || Number.class.isAssignableFrom(type)
        || CharSequence.class.isAssignableFrom(type)
        || Boolean.class == type
        || Character.class == type
        || Date.class.isAssignableFrom(type)
        || type.isEnum()
        || Class.class == type;
  }

  private String scalarText(Object value) {
    if (!limits.inert) {
      return String.valueOf(value);
    }
    if (value instanceof Enum) {
      return ((Enum<?>) value).name();
    }
    if (value instanceof Class) {
      return ((Class<?>) value).getName();
    }
    if (value instanceof Throwable) {
      return text(InertTypes.throwableText((Throwable) value));
    }
    return text(String.valueOf(value));
  }

  /**
   * Whether a map or iterable of this class is walked element by element. For a probe, see {@link
   * InertTypes#iterates}.
   */
  private boolean iterates(Class<?> type) {
    return !limits.inert || InertTypes.iterates(type);
  }

  private int size(Object value) {
    if (value instanceof Collection) {
      return ((Collection<?>) value).size();
    }
    int size = 0;
    for (Object ignored : (Iterable<?>) value) {
      size++;
    }
    return size;
  }

  /** True once a probe's node budget is gone; the OGNL surface has none. */
  private boolean spent() {
    return limits.inert && nodes >= limits.nodes;
  }

  /**
   * Keeps a string within the per-string cap and what is left of the event's text budget, from its
   * head AND its tail: a SQL statement cut at the head alone loses its WHERE clause, which is
   * usually the part being read.
   */
  private String text(String value) {
    long remaining = limits.eventChars - chars;
    int cap = (int) Math.max(0L, Math.min(limits.chars, remaining));
    if (value.length() <= cap) {
      chars += value.length();
      return value;
    }
    truncated = true;
    if (cap == 0) {
      return "…(" + value.length() + " chars not kept: this event's text budget is spent)";
    }
    chars += cap;
    int tail = cap / 2;
    int head = cap - tail;
    return value.substring(0, head)
        + "…(+"
        + (value.length() - cap)
        + " chars)…"
        + value.substring(value.length() - tail);
  }

  /**
   * A probe's cut at the depth hides what it did not expand as surely as a cap does; the OGNL
   * surface can expand it.
   */
  private void cutAtDepth(boolean hidAnything) {
    if (limits.inert && hidAnything) {
      truncated = true;
    }
  }

  private void appendTruncation(
      List<Node> children, int total, int included, String id, String path) {
    if (total > included) {
      if (limits.inert) {
        truncated = true;
      }
      children.add(
          scalar(
              id == null ? null : id + ".truncated",
              "(truncated)",
              path,
              (total - included) + " more values",
              null));
    }
  }

  private Node failure(String id, String path, String what, RuntimeException e) {
    truncated = true;
    return leaf(
        id == null ? null : id + ".failed",
        "(" + what + " failed)",
        path,
        "error",
        null,
        e.getClass().getName(),
        false);
  }

  private Node scalar(String id, String label, String path, String value, String type) {
    return account(new Node(id, label, "scalar", path, false, type, value, null, null));
  }

  private Node leaf(
      String id,
      String label,
      String path,
      String kind,
      String type,
      String raw,
      boolean expandable) {
    return account(new Node(id, label, kind, path, expandable, type, null, raw, null));
  }

  private Node branch(
      String id, String label, String path, String kind, String type, List<Node> children) {
    return account(new Node(id, label, kind, path, false, type, null, null, children));
  }

  /**
   * Charges a node against the walk's byte estimate: a fixed cost for the object and its list slot,
   * plus two bytes a character (the Java 8 target's strings are UTF-16). The type is not charged —
   * it is the class's own cached name.
   */
  private Node account(Node node) {
    bytes += 48 + 2L * (length(node.label) + length(node.value) + length(node.raw));
    return node;
  }

  private static int length(String value) {
    return value == null ? 0 : value.length();
  }

  private static String trail(String parent, String label) {
    return parent == null ? label : parent + "." + label;
  }

  private static String identity(Object value) {
    return value.getClass().getName() + "@" + Integer.toHexString(System.identityHashCode(value));
  }

  private static String escape(String value) {
    return value.replace("\\", "\\\\").replace("\"", "\\\"");
  }

  /**
   * One node, in the JSON shape the Go {@code tracestore.ObjectGraphNode} and clicky-ui's {@code
   * ObjectGraph} read. Built once by the walk and never changed after it returns, which is what
   * lets the agent publish it across threads.
   */
  public static final class Node {
    /** Null in a probe snapshot: the Go lowering assigns ids when it places the tree. */
    public final String id;

    public final String label;

    /** scalar, map, list, bean, identity, opaque or error. */
    public final String kind;

    /** The OGNL accessor that re-evaluates this node; null in a probe snapshot. */
    public final String path;

    public final boolean expandable;
    public final String type;
    public final String value;
    public final String raw;

    /** Null for a leaf. */
    public final List<Node> children;

    Node(
        String id,
        String label,
        String kind,
        String path,
        boolean expandable,
        String type,
        String value,
        String raw,
        List<Node> children) {
      this.id = id;
      this.label = label;
      this.kind = kind;
      this.path = path;
      this.expandable = expandable;
      this.type = type;
      this.value = value;
      this.raw = raw;
      this.children = children;
    }

    /**
     * The map the OGNL surface has always serialised, with its keys in the order it always wrote
     * them.
     */
    public Map<String, Object> toMap() {
      Map<String, Object> out = new java.util.LinkedHashMap<String, Object>();
      out.put("id", id);
      out.put("label", label);
      out.put("kind", kind);
      out.put("path", path);
      out.put("expandable", expandable);
      if (value != null) {
        out.put("value", value);
      }
      if (type != null) {
        out.put("type", type);
      }
      if (raw != null) {
        out.put("raw", raw);
      }
      if (children != null) {
        List<Map<String, Object>> rendered = new ArrayList<Map<String, Object>>(children.size());
        for (Node child : children) {
          rendered.add(child.toMap());
        }
        out.put("children", rendered);
      }
      return out;
    }
  }
}
