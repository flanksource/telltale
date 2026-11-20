package com.flanksource.telltale.observer.http;

import java.lang.reflect.Method;

final class HttpHandlers {
  private HttpHandlers() {}
  static void enrich(HttpOperation operation, Object handler) {
    if (HttpAccess.has(handler, "getHandler")) handler = HttpAccess.call(handler, "getHandler");
    if (!HttpAccess.has(handler, "getBeanType") || !HttpAccess.has(handler, "getMethod")) return;
    Object controller = HttpAccess.call(handler, "getBeanType"), method = HttpAccess.call(handler, "getMethod");
    if (controller instanceof Class && method instanceof Method) operation.attribute("handler", ((Class<?>) controller).getName() + "#" + ((Method) method).getName());
  }
}
