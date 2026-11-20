package com.flanksource.telltale.observer.http;

import java.io.*;
import java.lang.reflect.*;
import java.net.HttpURLConnection;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.concurrent.CompletionStage;

final class JdkHttp {
  private final HttpRegistry registry;
  private final ThreadLocal<Set<HttpOperation>> sending = ThreadLocal.withInitial(HashSet::new);
  JdkHttp(HttpRegistry registry) { this.registry = registry; }

  HttpObserver.Exit enter(String owner, String name, Object receiver, Object[] args) {
    if (receiver instanceof HttpURLConnection) {
      HttpURLConnection connection = (HttpURLConnection) receiver;
      if (name.equals("followRedirect0") && args.length == 3) {
        HttpRegistry.Binding binding = registry.binding(connection);
        if (binding == null) return null;
        binding.operation.response(((Number) args[1]).intValue(), Collections.singletonMap("Location", Collections.singletonList(args[0].toString())));
        registry.finish(binding.operation, null);
        return (result, error) -> result;
      }
      if (name.equals("writeRequests")) {
        registry.resumeConnection(connection);
        registry.begin("outgoing", connection.getRequestMethod(), connection.getURL().toString(), connection);
        return null;
      }
      return connection(connection, name);
    }
    if ((name.equals("send") || name.equals("sendAsync")) && args.length >= 2 && HttpAccess.has(args[0], "uri")) return send(name, receiver, args);
    if (name.equals("bodyPublisher")) {
      HttpRegistry.Binding binding = registry.binding(receiver);
      if (binding != null) return (result, error) -> {
        if (error != null || !(result instanceof Optional) || !((Optional<?>) result).isPresent()) return result;
        return Optional.of(publisher(((Optional<?>) result).get(), binding.operation, true));
      };
    }
    return null;
  }

  private HttpObserver.Exit connection(HttpURLConnection connection, String name) {
    if (!Arrays.asList("connect", "getInputStream", "getErrorStream", "getOutputStream", "getResponseCode", "disconnect").contains(name)) return null;
    if (registry.completedConnection(connection)) return null;
    HttpRegistry.Binding previous = registry.binding(connection);
    if (name.equals("disconnect")) {
      if (previous == null) return null;
      return (result, error) -> { registry.finish(previous.operation, error); return result; };
    }
    HttpOperation operation = registry.begin("outgoing", connection.getRequestMethod(), connection.getURL().toString(), connection);
    if (operation == null) return null;
    if (previous == null) operation.requestHeaders(connection.getRequestProperties());
    registry.push(operation);
    return (result, error) -> {
      try {
        if (error != null && !(error instanceof FileNotFoundException)) registry.finish(operation, error);
        else if (name.equals("disconnect")) registry.finish(operation, null);
        else if (name.equals("getInputStream") || name.equals("getErrorStream") || name.equals("getResponseCode")) {
          HttpRegistry.Binding latest = registry.binding(connection);
          HttpOperation response = latest == null ? operation : latest.operation;
          response.response(connection.getResponseCode(), HttpAccess.headers(connection.getHeaderFields()));
          if (result instanceof InputStream) registry.bind(result, response, false);
          if (response.method.equals("HEAD") || connection.getContentLengthLong() == 0) registry.finish(response, null);
        } else if (name.equals("getOutputStream")) registry.bind(result, operation, true);
        return result;
      } catch (IOException failure) { registry.finish(operation, failure); return result; }
      finally { registry.pop(operation); }
    };
  }

  private HttpObserver.Exit send(String name, Object client, Object[] args) {
    Object request = args[0];
    HttpOperation operation = registry.begin("outgoing", HttpAccess.text(request, "method"), HttpAccess.text(request, "uri"), request);
    if (operation == null) return null;
    if (!sending.get().add(operation)) return null;
    operation.requestHeaders(HttpAccess.headers(HttpAccess.call(request, "headers")));
    args[1] = bodyHandler(args[1], operation);
    registry.push(operation);
    return (result, error) -> {
      registry.pop(operation);
      sending.get().remove(operation);
      if (sending.get().isEmpty()) sending.remove();
      if (error != null) registry.finish(operation, error);
      else if (result instanceof CompletionStage) ((CompletionStage<?>) result).whenComplete((response, failure) -> {
        HttpCallbacks.observe(() -> { if (failure != null) registry.finish(operation, failure); else response(operation, response); });
      });
      else response(operation, result);
      return result;
    };
  }

  private void response(HttpOperation operation, Object response) {
    if (response == null || operation.finished()) return;
    operation.response(((Number) HttpAccess.call(response, "statusCode")).intValue(), HttpAccess.headers(HttpAccess.call(response, "headers")));
    Object body = HttpAccess.call(response, "body");
    if (body instanceof InputStream) { operation.streamingResponse = true; registry.bind(body, operation, false); }
  }

  private Object bodyHandler(Object original, HttpOperation operation) {
    return proxy(original, "java.net.http.HttpResponse$BodyHandler", (method, args) -> {
      Object result = invoke(original, method, args);
      if (!method.getName().equals("apply")) return result;
      return HttpCallbacks.decorate(result, () -> {
      operation.response(((Number) HttpAccess.call(args[0], "statusCode")).intValue(), HttpAccess.headers(HttpAccess.call(args[0], "headers")));
      operation.transportBody = true;
      return proxy(result, "java.net.http.HttpResponse$BodySubscriber", (callback, values) -> {
        if (callback.getName().equals("onNext")) HttpCallbacks.observe(() -> { for (Object buffer : (List<?>) values[0]) append(operation, false, (ByteBuffer) buffer); });
        try {
          Object value = invoke(result, callback, values);
          if (callback.getName().equals("getBody") && value instanceof CompletionStage) ((CompletionStage<?>) value).whenComplete((body, failure) -> HttpCallbacks.observe(() -> {
            if (body instanceof InputStream) { operation.streamingResponse = true; registry.bind(body, operation, false); }
          }));
          return value;
        }
        finally {
          HttpCallbacks.observe(() -> {
            if (callback.getName().equals("onComplete") && !operation.streamingResponse) registry.finish(operation, null);
            else if (callback.getName().equals("onError")) registry.finish(operation, (Throwable) values[0]);
          });
        }
      });
      });
    });
  }

  private Object publisher(Object original, HttpOperation operation, boolean request) {
    return proxy(original, "java.net.http.HttpRequest$BodyPublisher", (method, args) -> {
      if (method.getName().equals("subscribe")) {
        Object subscriber = args[0];
        args[0] = HttpCallbacks.decorate(subscriber, () -> proxy(subscriber, "java.util.concurrent.Flow$Subscriber", (callback, values) -> {
          if (callback.getName().equals("onNext") && values[0] instanceof ByteBuffer) HttpCallbacks.observe(() -> append(operation, request, (ByteBuffer) values[0]));
          return invoke(subscriber, callback, values);
        }));
      }
      return invoke(original, method, args);
    });
  }

  static void append(HttpOperation operation, boolean request, ByteBuffer buffer) {
    operation.append(request, buffer);
  }

  interface Invocation { Object invoke(Method method, Object[] args) throws Throwable; }
  private static Object proxy(Object original, String interfaceName, Invocation invocation) {
    try {
      Class<?> type = Class.forName(interfaceName, false, original.getClass().getClassLoader());
      return Proxy.newProxyInstance(type.getClassLoader(), new Class<?>[] {type}, (proxy, method, args) -> {
        if (method.getDeclaringClass() == Object.class) return invoke(original, method, args);
        return invocation.invoke(method, args == null ? new Object[0] : args);
      });
    } catch (ClassNotFoundException error) { throw new IllegalStateException("HTTP body API is unavailable: " + interfaceName, error); }
  }

  private static Object invoke(Object target, Method method, Object[] args) throws Throwable {
    try { return method.invoke(target, args); }
    catch (InvocationTargetException error) { throw error.getCause(); }
  }
}
