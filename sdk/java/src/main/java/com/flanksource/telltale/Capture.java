package com.flanksource.telltale;

public interface Capture extends AutoCloseable {
  byte[] dump() throws Exception;

  String contentType();

  String fileName();

  default boolean streaming() {
    return false;
  }

  default long intervalMillis() { return 250; }
  default long dropped() { return 0; }

  default boolean complete() {
    return false;
  }

  /**
   * Figures about the capture as a whole — what it withheld, rather than what it recorded — sent as
   * a {@code report} frame after each blob that changed them; null when the capture has none.
   */
  default java.util.Map<String, Object> report() {
    return null;
  }

  default void renew() throws Exception {}

  default void freeze() throws Exception {}

  default byte[] finish() throws Exception {
    freeze();
    return dump();
  }
}
