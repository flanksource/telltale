package com.flanksource.telltale.diag.hotpatch;

import com.flanksource.telltale.diag.Json;
import java.io.IOException;
import java.io.InputStream;
import java.util.Collections;
import java.util.Map;

/**
 * One JSON request body, read strictly.
 *
 * <p>Every accessor here refuses rather than guesses. These bodies name a class to rewrite in a
 * running JVM, so a field of the wrong type is a caller bug, and coercing it — a missing boolean
 * read as false, a string "30" read as thirty seconds — would turn that bug into a patch nobody
 * asked for.
 */
public final class JsonBody {

  private final Map<String, Object> fields;

  public JsonBody(Map<String, Object> fields) {
    this.fields = fields;
  }

  @SuppressWarnings("unchecked")
  static JsonBody read(InputStream in) throws IOException {
    Map<String, Object> parsed = Json.read(in, Map.class);
    if (parsed == null) {
      throw new IllegalArgumentException("a JSON object body is required");
    }
    return new JsonBody(parsed);
  }

  /**
   * The raw fields, for a callee that takes an open-ended option bag rather than a fixed set — the
   * probe object's per-probe tunables are the case. The agent ignores entries it does not
   * recognise, so an older CLI talking to a newer JVM still works.
   */
  Map<String, Object> fields() {
    return fields;
  }

  String required(String field) {
    String value = string(field);
    if (value == null) {
      throw new IllegalArgumentException(field + " is required");
    }
    return value;
  }

  /** A present, non-blank string, or null. Absent and blank mean the same thing. */
  String string(String field) {
    Object value = fields.get(field);
    if (value == null) return null;
    if (!(value instanceof String)) throw new IllegalArgumentException(field + " must be a string");
    if (((String) value).trim().isEmpty()) {
      return null;
    }
    // Source and a classpath are content, not identifiers: trimming them
    // would silently move the line numbers javac reports against.
    return "source".equals(field) || "classpath".equals(field)
        ? (String) value
        : ((String) value).trim();
  }

  boolean flag(String field) {
    Object value = fields.get(field);
    if (value == null) {
      return false;
    }
    if (!(value instanceof Boolean)) {
      throw new IllegalArgumentException(field + " must be true or false, got " + value);
    }
    return (Boolean) value;
  }

  /** Zero means "leave the default in place"; a negative is a caller bug. */
  int integer(String field) {
    Object value = fields.get(field);
    if (value == null) {
      return 0;
    }
    if (!(value instanceof Number)) {
      throw new IllegalArgumentException(field + " must be a number, got " + value);
    }
    int parsed = signedInteger(field);
    if (parsed < 0) {
      throw new IllegalArgumentException(field + " must not be negative, got " + parsed);
    }
    return parsed;
  }

  /**
   * Like {@link #integer}, but a negative is meaningful rather than a caller bug — {@code
   * leaseSeconds: -1} is how a caller says "leave this in place until I restore it", and refusing
   * it would make that unsayable.
   */
  int signedInteger(String field) {
    Object value = fields.get(field);
    if (value == null) {
      return 0;
    }
    if (!(value instanceof Number)) {
      throw new IllegalArgumentException(field + " must be a number, got " + value);
    }
    double parsed = ((Number) value).doubleValue();
    if (!Double.isFinite(parsed)
        || parsed != Math.rint(parsed)
        || parsed < Integer.MIN_VALUE
        || parsed > Integer.MAX_VALUE) {
      throw new IllegalArgumentException(field + " must be a 32-bit integer, got " + value);
    }
    return (int) parsed;
  }

  /** A required nested object, e.g. {@code probe}. */
  @SuppressWarnings("unchecked")
  JsonBody object(String field) {
    Object value = fields.get(field);
    if (!(value instanceof Map)) {
      throw new IllegalArgumentException(field + " is required, as an object");
    }
    return new JsonBody((Map<String, Object>) value);
  }

  /**
   * An optional nested object, absent reading as an empty one so a caller can ask through it
   * without branching first.
   *
   * <p>Still strict where it counts: a field of the wrong type is absent here rather than coerced,
   * and every accessor reached through the result refuses on its own terms.
   */
  @SuppressWarnings("unchecked")
  JsonBody optionalObject(String field) {
    Object value = fields.get(field);
    if (value != null && !(value instanceof Map))
      throw new IllegalArgumentException(field + " must be an object");
    return value instanceof Map
        ? new JsonBody((Map<String, Object>) value)
        : new JsonBody(Collections.<String, Object>emptyMap());
  }
}
