package com.flanksource.telltale.diag.fixture;

/**
 * A class to instrument and to splice into. Every method is here to exercise one shape the bytecode
 * has to get right: a static method and an instance one, a one-slot return and a two-slot one, a
 * void return, a method that throws, and a constructor — where an entry probe placed before the
 * {@code super()} call would be rejected by the verifier.
 */
public class ProbeFixture {

  private final String prefix;

  public ProbeFixture(String prefix) {
    this.prefix = prefix;
  }

  public static String greet(String name) {
    return "hello " + name;
  }

  public int add(int a, int b) {
    return a + b;
  }

  public long total(long seed) {
    return seed * 2L;
  }

  public void touch() {
    // Nothing: a void method still has an entry and a timed exit.
  }

  public String boom() {
    throw new IllegalStateException("boom");
  }

  public String prefixed(String value) {
    return prefix + value;
  }

  // Overloaded on purpose: a bare name must be refused as ambiguous rather
  // than resolved to whichever one the JVM happens to list first.
  public String describe(String value) {
    return "s:" + value;
  }

  public String describe(int value) {
    return "i:" + value;
  }

  /** A double return: two slots like long, but a different boxing path. */
  public double rate(double seed) {
    return seed * 2.0d;
  }

  /** Hands back exactly what it was given, so identity can be asserted. */
  public Object identity(Object value) {
    return value;
  }

  /**
   * A lambda in the body — the shape the source-splicing path has to refuse and this one handles
   * normally, because the lambda's body is a synthetic method in the same class file rather than
   * something the transform has to create.
   */
  public String labels(java.util.List<String> values) {
    java.util.List<String> sorted = new java.util.ArrayList<String>(values);
    java.util.Collections.sort(sorted);
    StringBuilder out = new StringBuilder();
    sorted.forEach(
        value -> {
          if (out.length() > 0) {
            out.append(',');
          }
          out.append(prefix).append(':').append(value);
        });
    return out.toString();
  }

  /** A method reference: a different invokedynamic shape from a lambda. */
  public String upper(java.util.List<String> values) {
    StringBuilder out = new StringBuilder();
    values.stream()
        .map(String::toUpperCase)
        .forEach(
            value -> {
              if (out.length() > 0) {
                out.append(',');
              }
              out.append(value);
            });
    return out.toString();
  }

  /**
   * Catches its own exception. The probe's catch-all is registered last, so it must never see what
   * this handles.
   */
  public String swallow() {
    try {
      throw new IllegalStateException("handled here");
    } catch (IllegalStateException e) {
      return "caught";
    }
  }

  /** Calls {@link #inner()} so nesting depth and parentage can be asserted. */
  public String outer() {
    return "outer:" + inner();
  }

  public String inner() {
    return "inner";
  }
}
