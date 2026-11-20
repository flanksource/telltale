package com.flanksource.telltale.observer.sql;

import java.util.*;

final class SqlProjection {
  private SqlProjection() {}

  static void validate(String kind, Map<String, Object> params) {
    Object connections = params.get("connections");
    if (connections != null) {
      if (!(connections instanceof List) || ((List<?>) connections).isEmpty())
        throw new IllegalArgumentException("SQL connections must contain connection names or *");
      for (Object connection : (List<?>) connections) {
        if (!(connection instanceof String) || ((String) connection).trim().isEmpty())
          throw new IllegalArgumentException("SQL connections must not contain blank names");
        if (connection.equals("self")) throw new IllegalArgumentException("connections self is Go-only; use * for JVM SQL");
      }
    }
    for (String key : Arrays.asList("minDurationNs", "minElapsedNs")) {
      Object value = params.get(key);
      if (value != null && (!(value instanceof Number) || ((Number) value).longValue() < 0))
        throw new IllegalArgumentException(key + " must be a nonnegative duration in nanoseconds");
    }
    if (params.get("minDuration") != null && !params.get("minDuration").toString().isEmpty() && !params.containsKey("minDurationNs"))
      throw new IllegalArgumentException("minDuration requires its parsed minDurationNs value");
    if (params.get("minElapsed") != null && !params.get("minElapsed").toString().isEmpty() && !params.containsKey("minElapsedNs"))
      throw new IllegalArgumentException("minElapsed requires its parsed minElapsedNs value");
    if (kind.equals("sql_top") && (Boolean.TRUE.equals(params.get("idle")) || params.containsKey("connection")))
      throw new IllegalArgumentException("connection and idle configure database polling, not JVM SQL top");
  }

  @SuppressWarnings("unchecked")
  static Map<String, Object> project(String kind, Map<String, Object> params, Map<String, Object> row) {
    Map<String, Object> details = kind.equals("sql_top") ? (Map<String, Object>) row.get("attributes") : row;
    if (details == null) throw new IllegalArgumentException("SQL top sample has no attributes");
    Object connections = params.get("connections");
    if (connections instanceof List && !((List<?>) connections).contains("*") && !((List<?>) connections).contains(details.get("connection"))) return null;
    Object minimum = params.get(kind.equals("sql_top") ? "minElapsedNs" : "minDurationNs");
    Object duration = row.get(kind.equals("sql_top") ? "elapsedNs" : "durationNs");
    if (minimum instanceof Number && duration instanceof Number && ((Number) duration).longValue() < ((Number) minimum).longValue()) return null;
    if (!matches(params.get("databases"), text(details, "database"), false)) return null;
    if (!matches(params.get("text"), text(details, "sql"), true)) return null;
    return row;
  }

  private static String text(Map<String, Object> row, String name) {
    Object value = row.get(name);
    return value == null ? "" : value.toString();
  }

  private static boolean matches(Object filters, String value, boolean substring) {
    if (filters == null) return true;
    if (!(filters instanceof List)) throw new IllegalArgumentException("SQL filters must be lists");
    boolean positive = false, matched = false;
    for (Object filter : (List<?>) filters) {
      if (!(filter instanceof String)) throw new IllegalArgumentException("SQL filter must be a string");
      for (String pattern : ((String) filter).split(",")) {
        pattern = pattern.trim();
        if (pattern.isEmpty()) continue;
        boolean exclude = pattern.startsWith("!");
        if (exclude) pattern = pattern.substring(1);
        else positive = true;
        if (pattern.isEmpty()) throw new IllegalArgumentException("SQL exclusion must name a pattern");
        if (substring && !pattern.contains("*")) pattern = "*" + pattern + "*";
        if (glob(value, pattern)) { if (exclude) return false; matched = true; }
      }
    }
    return !positive || matched;
  }

  private static boolean glob(String text, String pattern) {
    text = text.toLowerCase(Locale.ROOT);
    pattern = pattern.toLowerCase(Locale.ROOT);
    int input = 0, token = 0, star = -1, restart = 0;
    while (input < text.length()) {
      if (token < pattern.length() && pattern.charAt(token) == text.charAt(input)) { token++; input++; }
      else if (token < pattern.length() && pattern.charAt(token) == '*') { star = token++; restart = input; }
      else if (star >= 0) { token = star + 1; input = ++restart; }
      else return false;
    }
    while (token < pattern.length() && pattern.charAt(token) == '*') token++;
    return token == pattern.length();
  }
}
