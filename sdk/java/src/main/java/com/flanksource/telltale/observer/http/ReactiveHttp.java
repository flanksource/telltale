package com.flanksource.telltale.observer.http;

import java.nio.ByteBuffer;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;
import java.util.function.Function;

final class ReactiveHttp {
  private static final String CONTEXT = "com.flanksource.telltale.http.operation";
  private final HttpRegistry registry;
  ReactiveHttp(HttpRegistry registry) { this.registry = registry; }

  HttpObserver.Exit enter(String owner, String name, Object receiver, Object[] args) {
    if (name.equals("handle") && args.length == 2 && HttpAccess.has(args[0], "getURI") && HttpAccess.has(args[1], "getStatusCode"))
      return server(args[0], args[1]);
    if (name.equals("exchange") && args.length == 1 && HttpAccess.has(args[0], "url") && HttpAccess.has(args[0], "method")) {
      Object request = args[0];
      return (result, error) -> error != null ? result : client(result, request, HttpAccess.text(request, "method"), HttpAccess.text(request, "url"));
    }
    if (name.equals("connect") && args.length == 3 && args[1] instanceof java.net.URI && args[2] instanceof Function) {
      @SuppressWarnings("unchecked") Function<Object, Object> callback = (Function<Object, Object>) args[2];
      args[2] = (Function<Object, Object>) request -> HttpCallbacks.assemble(() -> callback.apply(request), () -> deferContext(receiver.getClass().getClassLoader(), context -> {
        HttpCallbacks.observe(() -> registry.bind(request, contextOperation(context), true));
        return callback.apply(request);
      }));
      return (result, error) -> error != null ? result : client(result, null, args[0].toString(), args[1].toString());
    }
    HttpRegistry.Binding binding = registry.binding(receiver);
    if (binding != null && name.equals("getBody") && (owner.startsWith("org.springframework.http.client.reactive.") || owner.startsWith("org.springframework.http.server.reactive."))) return (result, error) -> error == null ? body(result, binding.operation, binding.request, !binding.request) : result;
    if (binding != null && (name.equals("writeWith") || name.equals("writeAndFlushWith")) && args.length == 1) {
      if (name.equals("writeWith")) args[0] = body(args[0], binding.operation, binding.request, false);
      else args[0] = HttpAccess.call(flux(args[0]), "map", (Function<Object, Object>) part -> HttpCallbacks.decorate(part, () -> body(part, binding.operation, binding.request, false)));
      return (result, error) -> result;
    }
    if (name.equals("getHandler") && args.length == 1 && HttpAccess.has(args[0], "getRequest")) {
      Object exchange = args[0];
      HttpRegistry.Binding request = registry.binding(HttpAccess.call(exchange, "getRequest"));
      if (request == null) return null;
      return (result, error) -> error == null ? HttpAccess.call(result, "doOnNext", (Consumer<Object>) handler -> HttpCallbacks.observe(() -> { route(exchange, request.operation); HttpHandlers.enrich(request.operation, handler); })) : result;
    }
    if (binding != null && name.equals("releaseBody")) return (result, error) -> terminal(result, binding.operation);
    return null;
  }

  private HttpObserver.Exit server(Object request, Object response) {
    return (result, error) -> {
      if (error != null) return result;
      return deferContext(result.getClass().getClassLoader(), context -> HttpCallbacks.decorate(result, () -> {
        HttpOperation operation = registry.begin("incoming", HttpAccess.text(request, "getMethod"), HttpAccess.text(request, "getURI"), request);
        if (operation == null) return result;
        registry.bind(request, operation, true); registry.bind(response, operation, false);
        operation.requestHeaders(HttpAccess.headers(HttpAccess.call(request, "getHeaders")));
        Object pipeline = HttpAccess.call(result, "doOnSuccess", (Consumer<Object>) ignored -> HttpCallbacks.observe(() -> serverResponse(response, operation)));
        pipeline = HttpAccess.call(pipeline, "doOnError", (Consumer<Throwable>) failure -> HttpCallbacks.observe(() -> { serverResponse(response, operation); registry.finish(operation, failure); }));
        pipeline = HttpAccess.call(pipeline, "doFinally", (Consumer<Object>) signal -> HttpCallbacks.observe(() -> finish(operation, signal)));
        return context(pipeline, operation);
      }));
    };
  }

  private void serverResponse(Object response, HttpOperation operation) {
    Object code = HttpAccess.call(response, "getStatusCode");
    operation.response(code == null ? 200 : ((Number) HttpAccess.call(code, "value")).intValue(), HttpAccess.headers(HttpAccess.call(response, "getHeaders")));
  }

  private Object client(Object original, Object owner, String method, String url) {
    return deferContext(original.getClass().getClassLoader(), context -> HttpCallbacks.decorate(original, () -> {
      HttpOperation operation = owner == null ? contextOperation(context) : null;
      if (operation == null || !operation.direction.equals("outgoing")) operation = registry.create("outgoing", method, url);
      if (operation == null) return original;
      operation.directObserved = true;
      final HttpOperation selected = operation;
      AtomicBoolean headers = new AtomicBoolean();
      if (owner != null && HttpAccess.has(owner, "headers")) selected.requestHeaders(HttpAccess.headers(HttpAccess.call(owner, "headers")));
      Object pipeline = HttpAccess.call(original, "doOnNext", (Consumer<Object>) response -> HttpCallbacks.observe(() -> {
        headers.set(true); registry.bind(response, selected, false);
        Object code = HttpAccess.has(response, "getStatusCode") ? HttpAccess.call(response, "getStatusCode") : HttpAccess.call(response, "statusCode");
        Object responseHeaders = HttpAccess.has(response, "getHeaders") ? HttpAccess.call(response, "getHeaders") : HttpAccess.call(HttpAccess.call(response, "headers"), "asHttpHeaders");
        selected.response(((Number) HttpAccess.call(code, "value")).intValue(), HttpAccess.headers(responseHeaders));
      }));
      pipeline = HttpAccess.call(pipeline, "doOnError", (Consumer<Throwable>) failure -> HttpCallbacks.observe(() -> registry.finish(selected, failure)));
      pipeline = HttpAccess.call(pipeline, "doOnCancel", (Runnable) () -> HttpCallbacks.observe(() -> { if (!headers.get()) registry.finish(selected, new java.util.concurrent.CancellationException("HTTP request cancelled")); }));
      return context(pipeline, selected);
    }));
  }

  private Object body(Object original, HttpOperation operation, boolean request, boolean complete) {
    Object observed = HttpAccess.call(flux(original), "doOnNext", (Consumer<Object>) buffer -> HttpCallbacks.observe(() -> {
      if (buffer instanceof ByteBuffer) operation.append(request, (ByteBuffer) buffer);
      else if (HttpAccess.has(buffer, "asByteBuffer")) operation.append(request, (ByteBuffer) HttpAccess.call(buffer, "asByteBuffer"));
      else throw new IllegalArgumentException("unsupported HTTP data buffer " + buffer.getClass().getName());
    }));
    if (complete) observed = terminal(observed, operation);
    return observed;
  }

  private Object terminal(Object original, HttpOperation operation) {
    Object observed = HttpAccess.call(original, "doOnError", (Consumer<Throwable>) error -> HttpCallbacks.observe(() -> registry.finish(operation, error)));
    return HttpAccess.call(observed, "doFinally", (Consumer<Object>) signal -> HttpCallbacks.observe(() -> finish(operation, signal)));
  }

  private void finish(HttpOperation operation, Object signal) {
    registry.finish(operation, signal.toString().equalsIgnoreCase("cancel") ? new java.util.concurrent.CancellationException("HTTP response body cancelled") : null);
  }

  private static void route(Object exchange, HttpOperation operation) {
    Object route = HttpAccess.call(exchange, "getAttribute", "org.springframework.web.reactive.HandlerMapping.bestMatchingPattern");
    if (route != null) operation.attribute("route", route.toString());
  }
  private static Object context(Object publisher, HttpOperation operation) { return HttpAccess.call(publisher, "contextWrite", (Function<Object, Object>) context -> HttpCallbacks.decorate(context, () -> HttpAccess.call(context, "put", CONTEXT, operation))); }
  private static HttpOperation contextOperation(Object context) { return (HttpOperation) HttpAccess.call(context, "getOrDefault", CONTEXT, null); }
  private static Object deferContext(ClassLoader loader, Function<Object, Object> supplier) { return HttpAccess.callStatic(loader, "reactor.core.publisher.Mono", "deferContextual", supplier); }
  private static Object flux(Object publisher) { return HttpAccess.callStatic(publisher.getClass().getClassLoader(), "reactor.core.publisher.Flux", "from", publisher); }
}
