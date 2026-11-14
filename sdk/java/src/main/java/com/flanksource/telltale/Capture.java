package com.flanksource.telltale;

interface Capture extends AutoCloseable {
  byte[] dump() throws Exception;

  String contentType();

  String fileName();

  default boolean streaming() {
    return false;
  }

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
