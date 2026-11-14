package com.flanksource.telltale.diag.agent;

import com.fasterxml.jackson.core.JsonFactory;
import com.fasterxml.jackson.core.JsonGenerator;
import com.flanksource.telltale.diag.graph.ValueGraph;
import java.io.IOException;
import java.io.StringWriter;

/**
 * Renders one {@link ProbeEvent} as a line of JSON.
 *
 * <p>The JVM renders its own events so that the line travelling out is the line stored: the WAR
 * forwards it opaquely and the CLI appends it to a {@code .jsonl} byte for byte. That is what makes
 * replaying a saved capture a replay of what the JVM emitted rather than a re-render of a Go
 * struct's idea of it, and it removes three format conversions from the path.
 *
 * <p>Rendering happens at DRAIN, on a servlet thread — never at probe time. Two consequences
 * follow, and both are the point: JSON escaping never runs inside application's call path, and this
 * class's {@code <clinit>} (which builds a {@link JsonFactory}) never runs on an application
 * thread, where a failure would be an {@code ExceptionInInitializerError} in code that never asked
 * to be observed.
 *
 * <p>Jackson is relocated into this jar under {@code com.flanksource.telltale.diag.agent.shaded} —
 * see the {@code agentJar} task. The agent sits on the system class path, so an unrelocated copy
 * here would shadow application's own Jackson for every webapp in the JVM.
 */
final class ProbeJson {

  private static final JsonFactory FACTORY = new JsonFactory();

  private ProbeJson() {}

  /**
   * One event as one line, with absent fields omitted rather than nulled.
   *
   * <p>Never throws and never returns a malformed line. A consumer appends what comes back straight
   * into a file every later reader parses line by line, so a half-written object would corrupt the
   * archive for everyone; a line that says rendering failed keeps the file readable and the failure
   * visible.
   */
  static String line(ProbeEvent event) {
    StringWriter out = new StringWriter(256);
    try {
      JsonGenerator json = FACTORY.createGenerator(out);
      try {
        write(json, event);
      } finally {
        json.close();
      }
    } catch (IOException | RuntimeException e) {
      return fallback(event, e);
    }
    return out.toString();
  }

  private static void write(JsonGenerator json, ProbeEvent event) throws IOException {
    json.writeStartObject();
    json.writeNumberField("seq", event.seq);
    json.writeNumberField("ts", event.ts);
    json.writeStringField("kind", event.kind);
    json.writeStringField("key", event.key);
    json.writeNumberField("threadId", event.threadId);
    if (event.thread != null) {
      json.writeStringField("thread", event.thread);
    }
    json.writeNumberField("depth", event.depth);
    json.writeNumberField("frame", event.frame);
    if (event.parent != 0L) {
      json.writeNumberField("parent", event.parent);
    }
    if (event.className != null) {
      json.writeStringField("class", event.className);
    }
    if (event.method != null) {
      json.writeStringField("method", event.method);
    }
    if (event.descriptor != null) {
      json.writeStringField("descriptor", event.descriptor);
    }
    if (event.line != 0) {
      json.writeNumberField("line", event.line);
    }
    if (event.args != null) {
      json.writeArrayFieldStart("args");
      for (int i = 0; i < event.args.length; i++) {
        node(json, event.args[i]);
      }
      json.writeEndArray();
    }
    if (event.value != null) {
      json.writeFieldName("value");
      node(json, event.value);
    }
    if (event.error != null) {
      json.writeStringField("error", event.error);
    }
    if (event.durationNs != 0L) {
      json.writeNumberField("durationNs", event.durationNs);
    }
    if (event.allocBytes >= 0L) {
      // Written even when 0: a measured frame that allocated nothing is a
      // fact, and an absent field means it was not measured.
      json.writeNumberField("allocBytes", event.allocBytes);
    }
    if (event.gcCount >= 0L && event.gcPauseMs >= 0L) {
      json.writeNumberField("gcCount", event.gcCount);
      json.writeNumberField("gcPauseMs", event.gcPauseMs);
    }
    if (event.unwound != 0) {
      json.writeNumberField("unwound", event.unwound);
    }
    if (event.truncated) {
      json.writeBooleanField("truncated", true);
    }
    if (event.omitted > 0) {
      json.writeNumberField("omitted", event.omitted);
    }
    json.writeEndObject();
  }

  /**
   * One snapshot node, streamed field by field: the generator does the escaping here, on the drain
   * thread, so none of it ever ran inside the method that was observed. A probe's nodes carry no id
   * or path — the Go lowering assigns ids when it places the tree, and nothing can re-evaluate a
   * probe's object.
   */
  private static void node(JsonGenerator json, ValueGraph.Node node) throws IOException {
    json.writeStartObject();
    if (node.label != null) {
      json.writeStringField("label", node.label);
    }
    json.writeStringField("kind", node.kind);
    if (node.type != null) {
      json.writeStringField("type", node.type);
    }
    if (node.value != null) {
      json.writeStringField("value", node.value);
    }
    if (node.raw != null) {
      json.writeStringField("raw", node.raw);
    }
    if (node.children != null && !node.children.isEmpty()) {
      json.writeArrayFieldStart("children");
      for (ValueGraph.Node child : node.children) {
        node(json, child);
      }
      json.writeEndArray();
    }
    json.writeEndObject();
  }

  /**
   * A valid line standing in for one that could not be rendered, carrying enough to find the gap:
   * which sequence it was and what went wrong.
   */
  private static String fallback(ProbeEvent event, Throwable cause) {
    StringBuilder line = new StringBuilder(128);
    line.append("{\"seq\":")
        .append(event.seq)
        .append(",\"kind\":\"error\",\"error\":\"probe event could not be rendered: ")
        .append(cause.getClass().getName())
        .append("\"}");
    return line.toString();
  }
}
