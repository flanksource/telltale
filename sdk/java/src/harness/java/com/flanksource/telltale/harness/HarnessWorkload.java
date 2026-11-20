package com.flanksource.telltale.harness;

/** A method the end-to-end test traces with jvm_trace while the harness keeps calling it. */
public final class HarnessWorkload {
  private HarnessWorkload() {}

  public static double compute(long value) {
    return Math.sqrt(value);
  }
}
