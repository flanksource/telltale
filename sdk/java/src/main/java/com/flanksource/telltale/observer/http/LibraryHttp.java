package com.flanksource.telltale.observer.http;

import java.lang.reflect.*;
import java.net.URI;
import java.util.*;

final class LibraryHttp {
  private final HttpRegistry registry;
  private final ApacheAsyncHttp async;
  LibraryHttp(HttpRegistry registry) { this.registry = registry; this.async = new ApacheAsyncHttp(registry); }

  HttpObserver.Exit enter(String owner, String name, Object receiver, Object[] args) {
    if (name.equals("intercept") && owner.equals("okhttp3.internal.http.CallServerInterceptor") && args.length == 1) return attempt(args[0]);
    if ((name.equals("execute") || name.equals("enqueue")) && HttpAccess.has(receiver, "request")) return okhttp(name, receiver, args);
    if ((name.equals("execute") || name.equals("doExecute")) && owner.startsWith("org.apache.")) {
      HttpObserver.Exit asynchronous = async.enter(args);
      return asynchronous == null ? apache(receiver, args) : asynchronous;
    }
    if ((name.equals("createRequest") || name.equals("doExecute")) && owner.startsWith("org.springframework.")) return spring(name, args);
    HttpRegistry.Binding binding = registry.binding(receiver);
    if (binding == null) return null;
    if (name.equals("execute") && binding.request && HttpAccess.has(receiver, "getHeaders")) {
      binding.operation.requestHeaders(HttpAccess.headers(HttpAccess.call(receiver, "getHeaders")));
      registry.push(binding.operation);
      return (result, error) -> {
        registry.pop(binding.operation);
        if (error != null) registry.finish(binding.operation, error);
        else {
          Object status = HttpAccess.call(result, "getStatusCode");
          int code = status instanceof Number ? ((Number) status).intValue() : ((Number) HttpAccess.call(status, "value")).intValue();
          binding.operation.response(code, HttpAccess.headers(HttpAccess.call(result, "getHeaders")));
          registry.bind(result, binding.operation, false);
        }
        return result;
      };
    }
    if (name.equals("writeTo") && args.length == 1) registry.bind(args[0], binding.operation, true);
    return null;
  }

  private HttpObserver.Exit spring(String name, Object[] args) {
    URI url = null; String method = null;
    for (Object arg : args) {
      if (arg instanceof URI) url = (URI) arg;
      if (arg != null && arg.getClass().getName().equals("org.springframework.http.HttpMethod")) method = arg.toString();
    }
    if (url == null || method == null) return null;
    HttpOperation operation = registry.begin("outgoing", method, url.toString(), null);
    if (operation == null) return null;
    registry.push(operation);
    return (result, error) -> {
      registry.pop(operation);
      if (error != null) registry.finish(operation, error);
      else if (result != null && HttpAccess.has(result, "getHeaders")) registry.bind(result, operation, true);
      return result;
    };
  }

  private HttpObserver.Exit okhttp(String name, Object receiver, Object[] args) {
    Object request = HttpAccess.call(receiver, "request");
    HttpOperation operation = registry.begin("outgoing", HttpAccess.text(request, "method"), HttpAccess.text(request, "url"), receiver);
    if (operation == null) return null;
    operation.requestHeaders(HttpAccess.headers(HttpAccess.call(request, "headers")));
    registry.bind(HttpAccess.call(request, "body"), operation, true);
    if (name.equals("enqueue") && args.length == 1) {
      Object callback = args[0];
      try {
        Class<?> type = Class.forName("okhttp3.Callback", false, callback.getClass().getClassLoader());
        args[0] = Proxy.newProxyInstance(type.getClassLoader(), new Class<?>[] {type}, (proxy, method, values) -> {
          HttpCallbacks.observe(() -> {
            if (method.getName().equals("onResponse")) response(operation, values[1]);
            else if (method.getName().equals("onFailure")) registry.finish(operation, (Throwable) values[1]);
          });
          try { return method.invoke(callback, values); } catch (InvocationTargetException error) { throw error.getCause(); }
        });
      } catch (ClassNotFoundException error) { throw new IllegalStateException("OkHttp Callback API missing", error); }
    }
    registry.push(operation);
    return (result, error) -> {
      registry.pop(operation);
      if (error != null) registry.finish(operation, error);
      else if (name.equals("execute")) response(operation, result);
      return result;
    };
  }

  private void response(HttpOperation operation, Object response) {
    Object body = HttpAccess.call(response, "body");
    Object source = body == null ? null : HttpAccess.call(body, "source");
    HttpRegistry.Binding bound = registry.binding(source);
    if (bound == null) bound = registry.binding(body);
    if (bound != null) operation = bound.operation;
    if (operation.finished()) return;
    operation.response(((Number) HttpAccess.call(response, "code")).intValue(), HttpAccess.headers(HttpAccess.call(response, "headers")));
    registry.bind(body, operation, false); registry.bind(source, operation, false); registry.bind(response, operation, false);
    if (body == null || operation.method.equals("HEAD")) registry.finish(operation, null);
  }

  private HttpObserver.Exit attempt(Object chain) {
    Object request = HttpAccess.call(chain, "request");
    Object call = HttpAccess.call(chain, "call");
    HttpRegistry.Binding bound = registry.binding(call);
    HttpOperation previous = bound == null ? registry.current() : bound.operation;
    if (previous != null && !previous.finished() && !previous.firstNetworkAttempt()) { registry.finish(previous, null); previous = null; }
    HttpOperation operation = previous == null || previous.finished() ? registry.create("outgoing", HttpAccess.text(request, "method"), HttpAccess.text(request, "url")) : previous;
    if (operation == null) return null;
    operation.firstNetworkAttempt(); operation.directObserved = true;
    operation.requestHeaders(HttpAccess.headers(HttpAccess.call(request, "headers")));
    registry.bind(call, operation, false); registry.bind(HttpAccess.call(request, "body"), operation, true);
    registry.push(operation);
    return (result, error) -> {
      try { if (error != null) registry.finish(operation, error); else response(operation, result); return result; }
      finally { registry.pop(operation); }
    };
  }

  private HttpObserver.Exit apache(Object receiver, Object[] args) {
    Object request = null;
    for (Object argument : args) if (argument != null && (HttpAccess.has(argument, "getRequestLine") || HttpAccess.has(argument, "getMethod"))) { request = argument; break; }
    if (request == null) return null;
    Object line = HttpAccess.has(request, "getRequestLine") ? HttpAccess.call(request, "getRequestLine") : request;
    String method = HttpAccess.text(line, "getMethod");
    String url = HttpAccess.has(request, "getUri") ? HttpAccess.text(request, "getUri") : HttpAccess.text(line, "getUri");
    if (!URI.create(url).isAbsolute()) for (Object arg : args) if (arg != null && HttpAccess.has(arg, "toURI")) { url = HttpAccess.text(arg, "toURI") + url; break; }
    HttpOperation operation = registry.begin("outgoing", method, url, request);
    if (operation == null) return null;
    registry.bind(request, operation, true);
    operation.requestHeaders(HttpAccess.apacheHeaders(request));
    if (HttpAccess.has(request, "getEntity")) registry.bind(HttpAccess.call(request, "getEntity"), operation, true);
    registry.push(operation);
    return (result, error) -> {
      registry.pop(operation);
      if (error != null) registry.finish(operation, error);
      else if (result != null && (HttpAccess.has(result, "getCode") || HttpAccess.has(result, "getStatusLine"))) {
        Object code = HttpAccess.has(result, "getCode") ? HttpAccess.call(result, "getCode") : HttpAccess.call(HttpAccess.call(result, "getStatusLine"), "getStatusCode");
        operation.response(((Number) code).intValue(), HttpAccess.apacheHeaders(result)); registry.bind(result, operation, false);
      }
      return result;
    };
  }
}
