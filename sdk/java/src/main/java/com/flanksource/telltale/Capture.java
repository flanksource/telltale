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

  default void renew() throws Exception {}

  default void freeze() throws Exception {}

  default byte[] finish() throws Exception {
    freeze();
    return dump();
  }
}
