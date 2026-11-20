package com.flanksource.telltale.observer.http;

import java.lang.reflect.*;
import java.util.*;

final class ServletHttp {
  private static final String ATTRIBUTE = "com.flanksource.telltale.observer.http.operation";
  private final HttpRegistry registry;
  ServletHttp(HttpRegistry registry) { this.registry = registry; }

  HttpObserver.Exit enter(String owner, String name, Object receiver, Object[] args) {
    if ((name.equals("service") || name.equals("doFilter")) && args.length >= 2 && HttpAccess.has(args[0], "getRequestURL")) {
      Object request = args[0], response = args[1];
      Object stored = HttpAccess.call(request, "getAttribute", ATTRIBUTE);
      if (stored instanceof HttpOperation) {
        if (((HttpOperation) stored).finished()) return null;
        registry.bind(request, (HttpOperation) stored, true);
      } else if (stored != null) throw new IllegalStateException("HTTP observer servlet request attribute was replaced");
      String url = HttpAccess.text(request, "getRequestURL");
      Object query = HttpAccess.call(request, "getQueryString");
      if (query != null) url += "?" + query;
      HttpOperation operation = registry.begin("incoming", HttpAccess.text(request, "getMethod"), url, request);
      if (operation == null) return null;
      HttpAccess.call(request, "setAttribute", ATTRIBUTE, operation);
      boolean outermost = registry.current() != operation;
      registry.bind(request, operation, true);
      registry.bind(response, operation, false);
      operation.requestHeaders(HttpAccess.servletHeaders(request, true));
      registry.push(operation);
      return (result, error) -> {
        try {
          Object route = HttpAccess.call(request, "getAttribute", "org.springframework.web.servlet.HandlerMapping.bestMatchingPattern");
          if (route != null) operation.attribute("route", route.toString());
          operation.response(((Number) HttpAccess.call(response, "getStatus")).intValue(), HttpAccess.servletHeaders(response, false));
          if (outermost) {
            if (Boolean.TRUE.equals(HttpAccess.call(request, "isAsyncStarted"))) attach(HttpAccess.call(request, "getAsyncContext"), operation, response);
            else if (!operation.servletAsync) { try { registry.finish(operation, error); } finally { remove(request, operation); } }
          }
          return result;
        } finally { registry.pop(operation); }
      };
    }
    if (name.equals("startAsync")) {
      HttpRegistry.Binding binding = registry.binding(receiver);
      if (binding == null) return null;
      return (result, error) -> {
        if (error != null) registry.finish(binding.operation, error);
        else attach(result, binding.operation, HttpAccess.call(result, "getResponse"));
        return result;
      };
    }
    if (name.equals("getHandler") && args.length == 1 && HttpAccess.has(args[0], "getAttribute", "")) {
      HttpRegistry.Binding binding = registry.binding(args[0]);
      if (binding == null) return null;
      return (result, error) -> {
        Object route = HttpAccess.call(args[0], "getAttribute", "org.springframework.web.servlet.HandlerMapping.bestMatchingPattern");
        if (route != null) binding.operation.attribute("route", route.toString());
        if (error == null && result != null) HttpHandlers.enrich(binding.operation, result);
        return result;
      };
    }
    return null;
  }

  private void attach(Object context, HttpOperation operation, Object response) {
    HttpRegistry.Binding previous = registry.binding(context);
    if (previous != null && previous.operation == operation) return;
    registry.bind(context, operation, false);
    operation.servletAsync = true;
    Object request = HttpAccess.call(context, "getRequest");
    String namespace = HttpAccess.has(context, "getRequest") && context.getClass().getName().contains("jakarta") ? "jakarta" : namespace(context.getClass());
    try {
      Class<?> listenerType = Class.forName(namespace + ".servlet.AsyncListener", false, context.getClass().getClassLoader());
      Object listener = Proxy.newProxyInstance(listenerType.getClassLoader(), new Class<?>[] {listenerType}, (proxy, method, args) -> {
        if (method.getDeclaringClass() == Object.class) {
          if (method.getName().equals("hashCode")) return System.identityHashCode(proxy);
          if (method.getName().equals("equals")) return proxy == args[0];
          return "Telltale HTTP async listener";
        }
        HttpCallbacks.observe(() -> { if (method.getName().equals("onComplete")) {
          operation.response(((Number) HttpAccess.call(response, "getStatus")).intValue(), HttpAccess.servletHeaders(response, false));
          try { registry.finish(operation, null); } finally { remove(request, operation); }
        } else if (method.getName().equals("onStartAsync")) {
          Object next = HttpAccess.call(args[0], "getAsyncContext"); registry.unbind(next); attach(next, operation, response);
        }
        else if (method.getName().equals("onError")) {
          Object error = HttpAccess.call(args[0], "getThrowable");
          if (error != null) operation.attribute("error", error.toString());
        } else if (method.getName().equals("onTimeout")) operation.attribute("error", "servlet async timeout"); });
        return null;
      });
      HttpAccess.call(context, "addListener", listener);
    } catch (ClassNotFoundException error) { throw new IllegalStateException("servlet async listener API is unavailable", error); }
  }

  private static void remove(Object request, HttpOperation operation) {
    if (HttpAccess.call(request, "getAttribute", ATTRIBUTE) == operation) HttpAccess.call(request, "removeAttribute", ATTRIBUTE);
  }

  private static String namespace(Class<?> type) {
    for (Class<?> contract : type.getInterfaces()) {
      if (contract.getName().equals("jakarta.servlet.AsyncContext")) return "jakarta";
      if (contract.getName().equals("javax.servlet.AsyncContext")) return "javax";
    }
    if (type.getSuperclass() != null) return namespace(type.getSuperclass());
    throw new IllegalArgumentException("unknown servlet async context " + type.getName());
  }
}
