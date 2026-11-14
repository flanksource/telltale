package com.flanksource.telltale.diag.graph;

/**
 * What one {@link ValueGraph} walk may build. Immutable, so a probe resolves it once when it opens
 * and every thread shares it.
 */
public final class GraphLimits {
  final int depth;
  final int children;
  final int nodes;
  final int chars;
  final int eventChars;

  /** A probe snapshot: run no application code, take no monitor, and bound everything. */
  final boolean inert;

  private GraphLimits(
      int depth, int children, int nodes, int chars, int eventChars, boolean inert) {
    this.depth = depth;
    this.children = children;
    this.nodes = nodes;
    this.chars = chars;
    this.eventChars = eventChars;
    this.inert = inert;
  }

  /** The OGNL surface: {@code depth} levels (0..10), 100 children a container, text uncapped. */
  public static GraphLimits ognl(int depth) {
    return new GraphLimits(
        Math.max(0, Math.min(depth, 10)),
        100,
        Integer.MAX_VALUE,
        Integer.MAX_VALUE,
        Integer.MAX_VALUE,
        false);
  }

  /**
   * A probe snapshot: {@code depth} levels (1..10) and {@code children} a container; {@code nodes}
   * and {@code eventChars} across one whole event, shared by all of its arguments; {@code chars}
   * per string.
   */
  public static GraphLimits probe(int depth, int children, int nodes, int chars, int eventChars) {
    return new GraphLimits(
        Math.max(1, Math.min(depth, 10)),
        Math.max(1, children),
        Math.max(1, nodes),
        Math.max(1, chars),
        Math.max(1, eventChars),
        true);
  }

  public int depth() {
    return depth;
  }
}
