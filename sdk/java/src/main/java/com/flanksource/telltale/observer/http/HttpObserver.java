package com.flanksource.telltale.observer.http;

import com.flanksource.telltale.observer.ObserverAdapter;
import com.flanksource.telltale.observer.ObserverRuntime;
import java.util.*;

public final class HttpObserver implements ObserverAdapter {
  final HttpRegistry registry;
  private final HttpStreams streams;
  private final ServletHttp servlet;
  private final JdkHttp jdk;
  private final ReactiveHttp reactive;
  private final LibraryHttp libraries;
  private final Map<String, HttpOperation> nativeOperations = new java.util.concurrent.ConcurrentHashMap<>();

  public HttpObserver() {
    this(new HttpRegistry(() -> ObserverRuntime.armed("http") || ObserverRuntime.armed("http_top"),
        HttpObserver::bodyLimit,
        row -> ObserverRuntime.emit("http", row)));
  }

  private static int bodyLimit() {
    int maximum = 0;
    for (Map<String, Object> options : ObserverRuntime.captureOptions("http")) maximum = Math.max(maximum, HttpProjection.bodyLimit(options));
    return maximum;
  }

  HttpObserver(HttpRegistry registry) {
    this.registry = registry;
    streams = new HttpStreams(registry);
    servlet = new ServletHttp(registry);
    jdk = new JdkHttp(registry);
    reactive = new ReactiveHttp(registry);
    libraries = new LibraryHttp(registry);
  }

  @Override public String id() { return "http"; }
  @Override public Set<String> kinds() { return new LinkedHashSet<>(Arrays.asList("http", "http_top")); }

  @Override public boolean matchesType(String name, Set<String> hierarchy) {
    for (String type : hierarchy) {
      if (type.equals("java.io.InputStream") || type.equals("java.io.OutputStream") || type.equals("java.io.Reader") || type.equals("java.io.Writer")
          || type.equals("java.net.HttpURLConnection") || type.equals("java.net.http.HttpClient") || type.equals("java.net.http.HttpRequest")
          || type.equals("javax.servlet.Servlet") || type.equals("jakarta.servlet.Servlet")
          || type.equals("javax.servlet.Filter") || type.equals("jakarta.servlet.Filter")
          || type.equals("javax.servlet.ServletRequest") || type.equals("jakarta.servlet.ServletRequest")
          || type.equals("javax.servlet.ServletResponse") || type.equals("jakarta.servlet.ServletResponse")
          || type.equals("org.springframework.http.client.ClientHttpRequest") || type.equals("org.springframework.http.client.ClientHttpResponse")
          || type.equals("org.springframework.http.server.reactive.HttpHandler")
          || type.equals("org.apache.http.HttpEntity") || type.equals("org.apache.hc.core5.http.HttpEntity")
          || type.equals("org.apache.http.HttpResponse") || type.equals("org.apache.hc.core5.http.HttpResponse")
          || type.equals("org.apache.http.nio.ContentEncoder") || type.equals("org.apache.http.nio.ContentDecoder")
          || type.equals("org.apache.hc.core5.http.nio.DataStreamChannel")
          || type.equals("okio.BufferedSource") || type.equals("okio.BufferedSink") || type.equals("okhttp3.RequestBody") || type.equals("okhttp3.ResponseBody")) return true;
    }
    return name.startsWith("org.springframework.http.client.") || name.startsWith("org.springframework.http.server.reactive.")
        || name.equals("org.springframework.web.client.RestTemplate") || name.contains("DefaultRestClient$DefaultRequestBodyUriSpec")
        || name.contains("ExchangeFunctions$DefaultExchangeFunction") || name.contains("DefaultClientResponse")
        || name.startsWith("org.springframework.web.reactive.handler.") || name.startsWith("org.springframework.web.servlet.handler.")
        || name.startsWith("org.apache.http.impl.client.") || name.startsWith("org.apache.hc.client5.http.impl.")
        || name.startsWith("org.apache.http.impl.nio.client.")
        || name.equals("okhttp3.RealCall") || name.equals("okhttp3.internal.connection.RealCall") || name.equals("okhttp3.internal.http.CallServerInterceptor") || name.startsWith("okhttp3.ResponseBody");
  }

  @Override public boolean matchesMethod(String name, String descriptor) {
    return Arrays.asList("service", "doFilter", "startAsync", "getInputStream", "getOutputStream", "getErrorStream", "getReader", "getWriter",
        "read", "write", "close", "disconnect", "connect", "getResponseCode", "send", "sendAsync", "bodyPublisher", "intercept", "followRedirect0", "writeRequests",
        "doExecute", "createRequest", "execute", "doExecute", "enqueue", "exchange", "getBody", "releaseBody",
        "writeWith", "writeAndFlushWith", "handle", "getHandler", "getContent", "getEntity", "writeTo", "source", "byteStream", "charStream", "bytes", "string", "readUtf8", "readByteArray").contains(name);
  }

  @Override public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) {
    if (receiver == null) return null;
    if (Arrays.asList("read", "write", "close", "string", "bytes", "readUtf8", "readByteArray").contains(name)) {
      Object token = streams.enter(receiver, name, args);
      if (token != null) return (Exit) (result, error) -> { streams.exit(token, result, error); return result; };
    }
    Exit token = servlet.enter(owner, name, receiver, args);
    if (token == null) token = jdk.enter(owner, name, receiver, args);
    if (token == null) token = reactive.enter(owner, name, receiver, args);
    if (token == null) token = libraries.enter(owner, name, receiver, args);
    if (token == null) token = bindResult(name, receiver);
    return token;
  }

  private Exit bindResult(String name, Object receiver) {
    HttpRegistry.Binding binding = registry.binding(receiver);
    if (binding == null) return null;
    if (Arrays.asList("getInputStream", "getOutputStream", "getErrorStream", "getReader", "getWriter", "getBody", "getContent", "getEntity", "source", "byteStream", "charStream").contains(name))
      return (result, error) -> {
        if (error != null) registry.finish(binding.operation, error);
        else registry.bind(result, binding.operation, binding.request);
        return result;
      };
    return null;
  }

  @Override public Object onExit(Object token, Object result, Throwable error) { return ((Exit) token).apply(result, error); }

  @Override @SuppressWarnings("unchecked") public void onOperation(Map<String, Object> metadata) {
    if (!"http".equals(metadata.get("signal"))) return;
    Map<String, Object> attributes = (Map<String, Object>) metadata.get("attributes");
    String nativeID = metadata.get("operationId").toString();
    HttpOperation operation = nativeOperations.get(nativeID);
    if ("start".equals(metadata.get("phase"))) {
      operation = registry.current();
      Object request = metadata.get("request");
      if (request != null && HttpAccess.has(request, "request")) request = HttpAccess.call(request, "request");
      HttpRegistry.Binding binding = registry.binding(request);
      if (binding != null) operation = binding.operation;
      if (operation == null) {
        String method = value(attributes, "http.request.method", "http.method");
        String url = value(attributes, "url.full", "http.url");
        if (url.isEmpty() && request != null && HttpAccess.has(request, "getRequestURL")) url = HttpAccess.text(request, "getRequestURL");
        operation = registry.create("server".equals(metadata.get("kind")) ? "incoming" : "outgoing", method, url);
      }
      if (operation == null) return;
      registry.bind(request, operation, false);
      nativeOperations.put(nativeID, operation);
    }
    if (operation == null) return;
    for (String key : new String[] {"traceId", "spanId", "sampled"}) operation.attribute(key, metadata.get(key));
    operation.attribute("route", attributes.get("http.route"));
    Object status = attributes.containsKey("http.response.status_code") ? attributes.get("http.response.status_code") : attributes.get("http.status_code");
    if (status instanceof Number) operation.status(((Number) status).intValue());
    if (metadata.get("error") != null) operation.attribute("error", metadata.get("error"));
    if ("end".equals(metadata.get("phase"))) {
      nativeOperations.remove(nativeID);
      if (!operation.directObserved) registry.finish(operation, null);
    }
  }

  private static String value(Map<String, Object> attributes, String first, String second) {
    Object value = attributes.containsKey(first) ? attributes.get(first) : attributes.get(second);
    return value == null ? "" : value.toString();
  }
  @Override public List<Map<String, Object>> snapshot(String kind) { return kind.equals("http_top") ? registry.snapshot() : Collections.emptyList(); }
  @Override public Map<String, Object> project(String kind, Map<String, Object> params, Map<String, Object> row) { return HttpProjection.project(kind, params, row); }
  @Override public List<Map<String, Object>> terminalRows(String kind, Map<String, Object> params) {
    List<Map<String, Object>> rows = new ArrayList<>();
    if (kind.equals("http")) for (HttpOperation operation : registry.active.values()) {
      if (operation.finished()) continue;
      Map<String, Object> partial = operation.record();
      partial.put("_pending", true); partial.put("comment", "Capture ended before the HTTP exchange completed");
      rows.add(partial);
    }
    return rows;
  }
  @Override public void onDisarm(String kind) {
    if (kind.equals("http") ? !ObserverRuntime.armed("http_top") : !ObserverRuntime.armed("http")) { registry.clear(); nativeOperations.clear(); }
  }

  @Override public void validate(String kind, Map<String, Object> params) {
    Object direction = params.get("direction");
    if (direction != null && !Arrays.asList("", "both", "incoming", "outgoing").contains(direction.toString())) throw new IllegalArgumentException("HTTP direction must be incoming, outgoing or both");
    for (String key : new String[] {"hosts", "paths"}) if (params.get(key) instanceof Iterable) for (Object pattern : (Iterable<?>) params.get(key)) HttpGlob.compile(pattern.toString());
    if (kind.equals("http")) HttpProjection.bodyLimit(params);
  }

  interface Exit { Object apply(Object result, Throwable error); }
}
