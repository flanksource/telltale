package com.flanksource.telltale.observer.http;

import java.lang.reflect.*;
import java.net.URI;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.concurrent.CancellationException;

final class ApacheAsyncHttp {
  private final HttpRegistry registry;
  ApacheAsyncHttp(HttpRegistry registry) { this.registry = registry; }

  HttpObserver.Exit enter(Object[] args) {
    boolean matched = false;
    Exchange exchange = new Exchange();
    for (int i = 0; i < args.length; i++) {
      String contract = contract(args[i]);
      if (contract == null) continue;
      if (Proxy.isProxyClass(args[i].getClass()) && Proxy.getInvocationHandler(args[i]) instanceof Handler) continue;
      matched = true;
      Object original = args[i];
      args[i] = proxy(original, contract, (method, values) -> exchange.invoke(original, method, values, contract));
    }
    if (!matched) return null;
    return (result, error) -> { if (error != null) exchange.finish(error); return result; };
  }

  private final class Exchange {
    private HttpOperation operation = registry.current();
    private String target;

    Object invoke(Object original, Method method, Object[] args, String contract) throws Throwable {
      String name = method.getName();
      boolean producer = contract.contains("Producer");
      HttpCallbacks.observe(() -> {
      if (contract.contains("FutureCallback")) {
        if (name.equals("completed")) finish(null);
        else if (name.equals("cancelled")) finish(new CancellationException("Apache HTTP request cancelled"));
        else if (name.equals("failed")) finish((Throwable) args[0]);
      }
      if (producer && name.equals("generateRequest") && target == null && HttpAccess.has(original, "getTarget")) target = HttpAccess.text(HttpAccess.call(original, "getTarget"), "toURI");
      if (producer && name.equals("sendRequest") && args.length > 0) {
        Object channel = args[0];
        args[0] = proxy(channel, "org.apache.hc.core5.http.nio.RequestChannel", (callback, values) -> {
          if (callback.getName().equals("sendRequest")) HttpCallbacks.observe(() -> start(values[0]));
          return delegate(channel, callback, values);
        });
      }
      if (operation != null && (name.equals("produceContent") || name.equals("produce") || name.equals("consumeContent")) && args.length > 0)
        registry.bind(args[0], operation, producer);
      if (!producer && (name.equals("responseReceived") || name.equals("consumeResponse")) && args.length > 0) response(args[0]);
      if (!producer && name.equals("consumeResponse")) for (int i = 0; i < args.length; i++) {
        String callback = contract(args[i]);
        if (callback != null && callback.contains("FutureCallback")) {
          Object originalCallback = args[i];
          args[i] = proxy(originalCallback, callback, (terminal, values) -> invoke(originalCallback, terminal, values, callback));
        }
      }
      if (!producer && name.equals("consume") && args.length > 0 && args[0] instanceof ByteBuffer && operation != null)
        operation.append(false, (ByteBuffer) args[0]);
      });
      try {
        Object result = delegate(original, method, args);
        HttpCallbacks.observe(() -> {
        if (producer && name.equals("getTarget") && result != null) target = HttpAccess.text(result, "toURI");
        if (producer && name.equals("generateRequest")) start(result);
        if (!producer && (name.equals("responseCompleted") || name.equals("streamEnd") || name.equals("completed"))) finish(null);
        if (!producer && (name.equals("cancel") || name.equals("cancelled"))) finish(new CancellationException("Apache HTTP request cancelled"));
        if (name.equals("failed") && args.length > 0 && args[0] instanceof Throwable) finish((Throwable) args[0]);
        });
        return result;
      } catch (Throwable error) { HttpCallbacks.observe(() -> finish(error)); throw error; }
    }

    private void start(Object request) {
      Object line = HttpAccess.has(request, "getRequestLine") ? HttpAccess.call(request, "getRequestLine") : request;
      String uri = HttpAccess.has(request, "getUri") ? HttpAccess.text(request, "getUri") : HttpAccess.text(line, "getUri");
      if (target != null && !URI.create(uri).isAbsolute()) uri = target + uri;
      if (operation == null || operation.finished()) operation = registry.create("outgoing", HttpAccess.text(line, "getMethod"), uri);
      if (operation != null) {
        operation.directObserved = true; registry.bind(request, operation, true);
        operation.requestHeaders(HttpAccess.apacheHeaders(request));
        if (HttpAccess.has(request, "getEntity")) registry.bind(HttpAccess.call(request, "getEntity"), operation, true);
      }
    }

    private void response(Object response) {
      if (operation == null) return;
      Object code = HttpAccess.has(response, "getCode") ? HttpAccess.call(response, "getCode") : HttpAccess.call(HttpAccess.call(response, "getStatusLine"), "getStatusCode");
      operation.response(((Number) code).intValue(), HttpAccess.apacheHeaders(response)); registry.bind(response, operation, false);
    }

    private void finish(Throwable error) { if (operation != null) registry.finish(operation, error); }
  }

  private static String contract(Object value) {
    if (value == null) return null;
    for (String name : new String[] {"org.apache.http.nio.protocol.HttpAsyncRequestProducer", "org.apache.http.nio.protocol.HttpAsyncResponseConsumer",
        "org.apache.http.concurrent.FutureCallback", "org.apache.hc.core5.http.nio.AsyncRequestProducer", "org.apache.hc.core5.http.nio.AsyncResponseConsumer", "org.apache.hc.core5.concurrent.FutureCallback"})
      if (isA(value.getClass(), name)) return name;
    return null;
  }

  private static boolean isA(Class<?> type, String name) {
    if (type == null) return false;
    if (type.getName().equals(name)) return true;
    for (Class<?> parent : type.getInterfaces()) if (isA(parent, name)) return true;
    return isA(type.getSuperclass(), name);
  }

  private static Object proxy(Object original, String name, Invocation invocation) {
    try {
      Class<?> type = Class.forName(name, false, original.getClass().getClassLoader());
      return Proxy.newProxyInstance(type.getClassLoader(), new Class<?>[] {type}, new Handler(original, invocation));
    } catch (ClassNotFoundException error) { throw new IllegalStateException("Apache async API missing: " + name, error); }
  }
  private static Object delegate(Object original, Method method, Object[] args) throws Throwable {
    try { return method.invoke(original, args); } catch (InvocationTargetException error) { throw error.getCause(); }
  }
  interface Invocation { Object call(Method method, Object[] args) throws Throwable; }
  private static final class Handler implements InvocationHandler {
    private final Object original; private final Invocation invocation;
    Handler(Object original, Invocation invocation) { this.original = original; this.invocation = invocation; }
    public Object invoke(Object proxy, Method method, Object[] args) throws Throwable { return method.getDeclaringClass() == Object.class ? delegate(original, method, args) : invocation.call(method, args == null ? new Object[0] : args); }
  }
}
