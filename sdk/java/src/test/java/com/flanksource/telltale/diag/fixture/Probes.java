package com.flanksource.telltale.diag.fixture;

import com.flanksource.telltale.diag.Json;
import com.flanksource.telltale.diag.agent.DiagAgent;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Test-side view of the agent's probe buffers.
 *
 * <p>Reads back through the same {@code drain} the WAR uses and parses the same JSON lines, rather
 * than reaching into the ring directly. A test that inspected the internals would keep passing
 * after a change that broke the only path anything real uses.
 */
public final class Probes {

  private Probes() {}

  /** Opens a buffer with the JVM defaults, failing loudly if it cannot. */
  public static void open(String key) {
    open(key, Collections.<String, Object>emptyMap());
  }

  /** Opens a buffer with {@code options} overriding the JVM defaults. */
  public static void open(String key, Map<String, Object> options) {
    open(key, "test|" + key, options);
  }

  /**
   * Opens a buffer owned by {@code owner} — the {@code <loaderHash>|<class>} of the class being
   * instrumented, which is what a test about several spans on ONE class has to be able to say.
   */
  public static void open(String key, String owner, Map<String, Object> options) {
    Map<String, Object> opened = DiagAgent.openProbe(key, owner, options);
    if (!Boolean.TRUE.equals(opened.get("opened"))) {
      throw new IllegalStateException("could not open probe " + key + ": " + opened.get("error"));
    }
  }

  /** Every event recorded under {@code key}, parsed from its JSON line. */
  public static List<Map<String, Object>> events(String key) {
    return batch(key, 0L).events;
  }

  /** One cursored read, so a test can assert on what a reader would be told. */
  public static Batch batch(String key, long sinceSeq) {
    Map<String, Object> drained = DiagAgent.drain(key, sinceSeq, Integer.MAX_VALUE);
    Batch batch = new Batch();
    batch.dropped = number(drained.get("dropped"));
    batch.nextSeq = number(drained.get("nextSeq"));
    batch.suppressed = number(drained.get("suppressed"));
    Object lines = drained.get("events");
    if (lines instanceof List) {
      for (Object line : (List<?>) lines) {
        batch.events.add(parse(String.valueOf(line)));
      }
    }
    return batch;
  }

  /** Closes every probe; call from a test's setup so one cannot leak into the next. */
  public static void closeAll() {
    DiagAgent.closeAllProbes();
  }

  /** The kinds recorded under {@code key}, in order — the usual coarse assertion. */
  public static List<String> kinds(String key) {
    List<String> kinds = new ArrayList<String>();
    for (Map<String, Object> event : events(key)) {
      kinds.add(String.valueOf(event.get("kind")));
    }
    return kinds;
  }

  /** The first event of {@code kind}, or null when the probe recorded none. */
  public static Map<String, Object> first(String key, String kind) {
    for (Map<String, Object> event : events(key)) {
      if (kind.equals(event.get("kind"))) {
        return event;
      }
    }
    return null;
  }

  /**
   * A value-graph node as the JSON line carried it, failing loudly when the field holds anything
   * else.
   */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> node(Object value) {
    if (!(value instanceof Map)) {
      throw new AssertionError("not a value-graph node: " + value);
    }
    return (Map<String, Object>) value;
  }

  /** A scalar node's text: what a String, number or boolean rendered as. */
  public static String scalar(Object node) {
    return String.valueOf(node(node).get("value"));
  }

  /** Each argument's text, for a method whose arguments are all scalars. */
  public static List<String> argValues(Map<String, Object> enter) {
    List<String> values = new ArrayList<String>();
    for (Map<String, Object> arg : args(enter)) {
      values.add(scalar(arg));
    }
    return values;
  }

  /** The argument graphs an enter event carried, in position order. */
  @SuppressWarnings("unchecked")
  public static List<Map<String, Object>> args(Map<String, Object> enter) {
    Object args = enter.get("args");
    if (!(args instanceof List)) {
      throw new AssertionError("no args on " + enter);
    }
    return (List<Map<String, Object>>) args;
  }

  /** The child of {@code node} labelled {@code label}. */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> child(Map<String, Object> node, String label) {
    Object children = node.get("children");
    if (children instanceof List) {
      for (Object child : (List<?>) children) {
        if (label.equals(((Map<String, Object>) child).get("label"))) {
          return (Map<String, Object>) child;
        }
      }
    }
    throw new AssertionError("no child " + label + " in " + node);
  }

  public static final class Batch {
    public final List<Map<String, Object>> events = new ArrayList<Map<String, Object>>();
    public long dropped;
    public long nextSeq;
    public long suppressed;
  }

  private static long number(Object value) {
    return value instanceof Number ? ((Number) value).longValue() : 0L;
  }

  @SuppressWarnings("unchecked")
  private static Map<String, Object> parse(String line) {
    try {
      return Json.read(new ByteArrayInputStream(line.getBytes(StandardCharsets.UTF_8)), Map.class);
    } catch (IOException e) {
      // A line that will not parse is a defect in the renderer, and every
      // consumer appends these straight into a file it later reads back —
      // so surface it rather than letting a test see an empty event.
      throw new UncheckedIOException("probe event is not valid JSON: " + line, e);
    }
  }

  /** Convenience for the common "one override" case. */
  public static Map<String, Object> options(String name, Object value) {
    Map<String, Object> options = new HashMap<String, Object>();
    options.put(name, value);
    return options;
  }
}
