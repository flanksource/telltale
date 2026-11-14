package com.flanksource.telltale;

/** Authorization scope an adapter caller needs for an operation, capture or route. */
public enum Scope {
  READ("jvm:read"),
  TRACE("jvm:trace"),
  INSTRUMENT("jvm:instrument"),
  EVAL("jvm:eval"),
  WRITE("jvm:write");

  private final String wire;

  Scope(String wire) {
    this.wire = wire;
  }

  public String wire() {
    return wire;
  }

  public boolean mutates() {
    return this != READ;
  }

  public static Scope forCapture(String kind) {
    if (kind == null) throw new IllegalArgumentException("capture kind is required");
    switch (kind) {
      case "jvm_jfr":
      case "jvm_trace":
      case "jvm_notifications":
        return TRACE;
      case "jvm_redefine":
        return INSTRUMENT;
      case "jvm_ognl":
        return EVAL;
      default:
        throw new IllegalArgumentException("unknown capture kind: " + kind);
    }
  }

  /** Scope of an extension operation, which declares only whether it mutates. */
  static Scope forExtension(boolean mutates) {
    return mutates ? WRITE : READ;
  }
}
