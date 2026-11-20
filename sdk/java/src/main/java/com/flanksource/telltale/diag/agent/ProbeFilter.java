package com.flanksource.telltale.diag.agent;

import com.flanksource.telltale.diag.graph.ValueGraph;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import java.util.regex.PatternSyntaxException;

/**
 * Which ROOT calls a probe publishes: {@code minCost}, {@code exceptionsOnly} and {@code match}.
 *
 * <p>A root call is the outermost invocation of the probe on its thread; everything it records
 * while it runs — nested targets and call sites — is held on that thread and published or dropped
 * with it, so a kept call arrives whole and a dropped one leaves nothing behind but the {@code
 * filtered} count.
 *
 * <p>Every predicate is resolved and checked when the probe opens, on a servlet thread. A predicate
 * the probe could not evaluate — a match on arguments the method is not recording, a regex that
 * does not compile, a duration that does not parse — refuses the open: a filter that quietly passed
 * everything, or nothing, would read as a quiet method.
 *
 * <p>Immutable, and holds no static state: it is consulted on application threads.
 */
final class ProbeFilter {

  static final String ON_ARG = "arg";
  static final String ON_RETURN = "return";

  final long minCostNanos;
  final boolean exceptionsOnly;
  private final Predicate[] argPredicates;
  private final Predicate[] returnPredicates;

  private ProbeFilter(
      long minCostNanos, boolean exceptionsOnly, List<Predicate> args, List<Predicate> returns) {
    this.minCostNanos = minCostNanos;
    this.exceptionsOnly = exceptionsOnly;
    this.argPredicates = args.toArray(new Predicate[0]);
    this.returnPredicates = returns.toArray(new Predicate[0]);
  }

  /**
   * The filter {@code options} ask for, or null when they ask for none.
   *
   * @throws IllegalArgumentException naming the option that cannot be honoured
   */
  static ProbeFilter resolve(Map<String, Object> options) {
    if (options == null) {
      return null;
    }
    Object minCost = options.get("minCost");
    Object exceptionsOnly = options.get("exceptionsOnly");
    Object match = options.get("match");
    if (minCost == null && exceptionsOnly == null && match == null) {
      return null;
    }
    long nanos = minCost == null ? 0L : parseDuration(String.valueOf(minCost));
    if (nanos < 0L) {
      throw new IllegalArgumentException("minCost must not be negative, got " + minCost);
    }
    if (exceptionsOnly != null && !(exceptionsOnly instanceof Boolean)) {
      throw new IllegalArgumentException("exceptionsOnly must be a boolean, got " + exceptionsOnly);
    }
    List<Predicate> args = new ArrayList<Predicate>();
    List<Predicate> returns = new ArrayList<Predicate>();
    for (Predicate predicate : predicates(match)) {
      requireRecorded(options, predicate);
      (ON_ARG.equals(predicate.on) ? args : returns).add(predicate);
    }
    return new ProbeFilter(nanos, Boolean.TRUE.equals(exceptionsOnly), args, returns);
  }

  private static List<Predicate> predicates(Object match) {
    List<Predicate> predicates = new ArrayList<Predicate>();
    if (match == null) {
      return predicates;
    }
    if (!(match instanceof List)) {
      throw new IllegalArgumentException("match must be a list of predicates, got " + match);
    }
    for (Object raw : (List<?>) match) {
      if (!(raw instanceof Map)) {
        throw new IllegalArgumentException("each match predicate must be an object, got " + raw);
      }
      predicates.add(Predicate.parse((Map<?, ?>) raw));
    }
    return predicates;
  }

  /** A predicate on a value the probe does not record could never be true. */
  private static void requireRecorded(Map<String, Object> options, Predicate predicate) {
    String recorded = ON_ARG.equals(predicate.on) ? "args" : "return";
    if (!Boolean.TRUE.equals(options.get(recorded))) {
      throw new IllegalArgumentException(
          "match on "
              + predicate.on
              + " needs "
              + recorded
              + ":true, or the probe never renders the value it would test");
    }
    Object mode = options.get("arg-mode");
    if (mode != null && !ProbeConfig.ARGS_VALUE.equalsIgnoreCase(String.valueOf(mode))) {
      throw new IllegalArgumentException(
          "match needs arg-mode value; arg-mode " + mode + " renders no value to test");
    }
  }

  boolean hasArgPredicates() {
    return argPredicates.length > 0;
  }

  boolean hasReturnPredicates() {
    return returnPredicates.length > 0;
  }

  /** Every argument predicate holds on the root call's rendered arguments. */
  boolean matchesArgs(ValueGraph.Node[] args) {
    for (Predicate predicate : argPredicates) {
      ValueGraph.Node root =
          args == null || predicate.index >= args.length ? null : args[predicate.index];
      if (!predicate.test(root)) {
        return false;
      }
    }
    return true;
  }

  /** Every return predicate holds on the root call's rendered return value. */
  boolean matchesReturn(ValueGraph.Node value) {
    for (Predicate predicate : returnPredicates) {
      if (!predicate.test(value)) {
        return false;
      }
    }
    return true;
  }

  /**
   * Whether a finished root call is published.
   *
   * @param returnMatched what {@link #matchesReturn} said when it returned; false when it threw
   */
  boolean keeps(long elapsedNanos, boolean threw, boolean returnMatched) {
    if (elapsedNanos < minCostNanos || (exceptionsOnly && !threw)) {
      return false;
    }
    return !hasReturnPredicates() || (returnMatched && !threw);
  }

  /** A canonical description, so a join that asks for a different filter is refused. */
  String describe() {
    StringBuilder out =
        new StringBuilder("minCost=").append(minCostNanos).append("ns exceptionsOnly=");
    out.append(exceptionsOnly).append(" match=[");
    for (Predicate predicate : argPredicates) {
      out.append(predicate).append(' ');
    }
    for (Predicate predicate : returnPredicates) {
      out.append(predicate).append(' ');
    }
    return out.append(']').toString();
  }

  /**
   * Parses a Go {@code time.Duration} string — {@code 250ms}, {@code 1.5s}, {@code 1m30s} — into
   * nanoseconds. The CLI and the webapp send what a person typed, and Go is what validated it.
   */
  static long parseDuration(String text) {
    String value = text == null ? "" : text.trim();
    if ("0".equals(value)) {
      return 0L;
    }
    if (value.isEmpty()) {
      throw new IllegalArgumentException("minCost must be a duration such as 250ms, got \"\"");
    }
    double total = 0;
    int i = 0;
    while (i < value.length()) {
      int start = i;
      while (i < value.length() && (Character.isDigit(value.charAt(i)) || value.charAt(i) == '.')) {
        i++;
      }
      int unitStart = i;
      while (i < value.length() && !Character.isDigit(value.charAt(i)) && value.charAt(i) != '.') {
        i++;
      }
      if (start == unitStart || unitStart == i) {
        throw new IllegalArgumentException(
            "minCost must be a duration such as 250ms or 1m30s, got \"" + text + "\"");
      }
      total +=
          Double.parseDouble(value.substring(start, unitStart))
              * unitNanos(value.substring(unitStart, i), text);
    }
    return (long) total;
  }

  private static long unitNanos(String unit, String text) {
    if ("ns".equals(unit)) return 1L;
    if ("us".equals(unit) || "µs".equals(unit)) return 1_000L;
    if ("ms".equals(unit)) return 1_000_000L;
    if ("s".equals(unit)) return 1_000_000_000L;
    if ("m".equals(unit)) return 60_000_000_000L;
    if ("h".equals(unit)) return 3_600_000_000_000L;
    throw new IllegalArgumentException(
        "minCost has unknown unit \"" + unit + "\" in \"" + text + "\"; use ns, us, ms, s, m or h");
  }

  /** One {@code {on, index, path, op, value}} test on a rendered value. */
  private static final class Predicate {
    final String on;
    final int index;
    final String[] path;
    final String op;
    final String value;
    final Pattern pattern;

    private Predicate(String on, int index, String[] path, String op, String value) {
      this.on = on;
      this.index = index;
      this.path = path;
      this.op = op;
      this.value = value;
      this.pattern = "regex".equals(op) ? compile(value) : null;
    }

    static Predicate parse(Map<?, ?> raw) {
      String on = string(raw, "on");
      if (!ON_ARG.equals(on) && !ON_RETURN.equals(on)) {
        throw new IllegalArgumentException("match.on must be arg or return, got " + on);
      }
      String op = string(raw, "op");
      if (!"equals".equals(op) && !"contains".equals(op) && !"regex".equals(op)) {
        throw new IllegalArgumentException(
            "match.op must be equals, contains or regex, got " + op);
      }
      Object value = raw.get("value");
      if (!(value instanceof String)) {
        throw new IllegalArgumentException("match.value must be a string, got " + value);
      }
      int index = index(raw.get("index"), on);
      Object path = raw.get("path");
      String[] segments =
          path == null || String.valueOf(path).isEmpty()
              ? new String[0]
              : String.valueOf(path).split("\\.");
      return new Predicate(on, index, segments, op, (String) value);
    }

    private static int index(Object raw, String on) {
      if (raw == null) {
        if (ON_ARG.equals(on)) {
          throw new IllegalArgumentException("match on arg needs index, the argument position");
        }
        return 0;
      }
      if (!(raw instanceof Number) || ((Number) raw).intValue() < 0) {
        throw new IllegalArgumentException("match.index must be a non-negative integer, got " + raw);
      }
      int index = ((Number) raw).intValue();
      if (ON_RETURN.equals(on) && index != 0) {
        throw new IllegalArgumentException("match on return takes no index, got " + raw);
      }
      return index;
    }

    private static String string(Map<?, ?> raw, String name) {
      Object value = raw.get(name);
      return value == null ? null : String.valueOf(value);
    }

    private static Pattern compile(String regex) {
      try {
        return Pattern.compile(regex);
      } catch (PatternSyntaxException e) {
        throw new IllegalArgumentException(
            "match regex " + regex + " does not compile: " + e.getDescription(), e);
      }
    }

    /** False when the path names nothing the snapshot holds: the value is not there to match. */
    boolean test(ValueGraph.Node root) {
      String text = text(walk(root));
      if (text == null) {
        return false;
      }
      if ("equals".equals(op)) {
        return text.equals(value);
      }
      if ("contains".equals(op)) {
        return text.contains(value);
      }
      return pattern.matcher(text).find();
    }

    private ValueGraph.Node walk(ValueGraph.Node root) {
      ValueGraph.Node node = root;
      for (String segment : path) {
        node = node == null ? null : child(node, segment);
      }
      return node;
    }

    private static ValueGraph.Node child(ValueGraph.Node node, String label) {
      if (node.children == null) {
        return null;
      }
      for (ValueGraph.Node child : node.children) {
        if (label.equals(child.label)) {
          return child;
        }
      }
      return null;
    }

    /** What a node rendered as: its value text, else its identity ({@code raw}). */
    private static String text(ValueGraph.Node node) {
      if (node == null) {
        return null;
      }
      return node.value != null ? node.value : node.raw;
    }

    @Override
    public String toString() {
      return on + "[" + index + "]" + String.join(".", path) + " " + op + " " + value;
    }
  }
}
