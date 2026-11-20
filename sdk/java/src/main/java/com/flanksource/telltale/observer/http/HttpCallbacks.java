package com.flanksource.telltale.observer.http;

import com.flanksource.telltale.observer.ObserverRuntime;
import java.util.function.Supplier;

final class HttpCallbacks {
  private HttpCallbacks() {}

  static void observe(Runnable callback) {
    try { callback.run(); }
    catch (Throwable error) { failed(error); }
  }

  static <T> T decorate(T original, Supplier<T> callback) {
    try { return callback.get(); }
    catch (Throwable error) { failed(error); return original; }
  }

  static <T> T assemble(Supplier<T> original, Supplier<T> callback) {
    try { return callback.get(); }
    catch (Throwable error) { failed(error); }
    return original.get();
  }

  private static void failed(Throwable error) {
    ObserverRuntime.fail("http", error);
    ObserverRuntime.fail("http_top", error);
  }
}
