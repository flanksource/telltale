package com.flanksource.telltale.observer.http;

import java.io.ByteArrayOutputStream;
import java.net.URI;
import java.nio.ByteBuffer;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.atomic.AtomicLong;
import java.util.function.Consumer;

final class HttpOperation {
  private static final AtomicLong NEXT = new AtomicLong();
  final String id = "http/" + NEXT.incrementAndGet();
  final String direction;
  final String method;
  final String url;
  final long startedNanos = System.nanoTime();
  final Instant started = Instant.now();
  volatile boolean directObserved;
  private final int bodyLimit;
  private final Consumer<Map<String, Object>> completed;
  private final Map<String, Object> attributes = new LinkedHashMap<>();
  private final ByteArrayOutputStream requestBody = new ByteArrayOutputStream();
  private final ByteArrayOutputStream responseBody = new ByteArrayOutputStream();
  private Map<String, List<String>> requestHeaders = Collections.emptyMap();
  private Map<String, List<String>> responseHeaders = Collections.emptyMap();
  private long requestBytes;
  private long responseBytes;
  private long elapsedNanos;
  private int status;
  private boolean finished;
  private String failure;
  private int bodyCallbacks;
  private boolean pendingTerminal;
  private Throwable pendingError;

  HttpOperation(String direction, String method, String url, int bodyLimit, Consumer<Map<String, Object>> completed) {
    this.direction = direction;
    this.method = method;
    this.url = sanitizedURL(url);
    this.bodyLimit = bodyLimit;
    this.completed = completed;
    attributes.put("direction", direction);
    attributes.put("method", method);
    attributes.put("url", this.url);
    attributes.put("startedAt", started.toString());
    attributes.put("threadId", Thread.currentThread().getId());
    attributes.put("thread", Thread.currentThread().getName());
    attributes.put("phase", "request");
    if (!this.url.isEmpty()) attributes.put("host", URI.create(this.url).getHost());
  }

  synchronized void attribute(String name, Object value) {
    if (value != null) attributes.put(name, value);
  }

  synchronized void requestHeaders(Map<String, List<String>> headers) { requestHeaders = redacted(headers); }

  synchronized void response(int code, Map<String, List<String>> headers) {
    status = code;
    responseHeaders = redacted(headers);
    attributes.put("phase", "response");
  }

  synchronized void status(int code) { status = code; }

  synchronized void append(boolean request, byte[] bytes, int offset, int length) {
    if (finished || length <= 0) return;
    ByteArrayOutputStream body = request ? requestBody : responseBody;
    if (request) requestBytes += length; else responseBytes += length;
    int retained = Math.min(length, bodyLimit - body.size());
    if (retained > 0) body.write(bytes, offset, retained);
  }

  synchronized void append(boolean request, ByteBuffer original) {
    if (finished) return;
    int count = original.remaining();
    if (request) requestBytes += count; else responseBytes += count;
    ByteArrayOutputStream body = request ? requestBody : responseBody;
    int retained = Math.min(count, bodyLimit - body.size());
    if (retained <= 0) return;
    ByteBuffer view = original.asReadOnlyBuffer();
    byte[] bytes = new byte[retained]; view.get(bytes); body.write(bytes, 0, bytes.length);
  }

  boolean finish(Throwable error) {
    synchronized (this) {
      if (finished) return true;
      if (bodyCallbacks > 0) { pendingTerminal = true; if (error != null) pendingError = error; return false; }
      finished = true;
      elapsedNanos = System.nanoTime() - startedNanos;
      if (error != null) failure = error.getClass().getName() + ": " + error.getMessage();
      attributes.put("phase", error == null ? "complete" : "error");
    }
    completed.accept(record());
    return true;
  }

  synchronized boolean finished() { return finished; }
  synchronized void retire() { finished = true; elapsedNanos = System.nanoTime() - startedNanos; }
  synchronized void holdBody() { bodyCallbacks++; }
  synchronized boolean releaseBody() { if (--bodyCallbacks < 0) throw new IllegalStateException("HTTP body callbacks are unbalanced"); return bodyCallbacks == 0 && pendingTerminal; }
  synchronized Throwable pendingError() { return pendingError; }
  private boolean networkAttempt;
  synchronized boolean firstNetworkAttempt() { if (networkAttempt) return false; networkAttempt = true; return true; }
  volatile boolean transportBody;
  volatile boolean streamingResponse;
  volatile boolean servletAsync;

  synchronized Map<String, Object> snapshot() {
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("operationId", id);
    row.put("elapsedNs", elapsed());
    row.put("sampledAtNanos", com.flanksource.telltale.observer.ObserverRuntime.epochNanos());
    row.put("attributes", new LinkedHashMap<>(attributes));
    for (String key : new String[] {"traceId", "spanId"}) if (attributes.containsKey(key)) row.put(key, attributes.get(key));
    return row;
  }

  synchronized Map<String, Object> record() {
    Map<String, Object> row = new LinkedHashMap<>(attributes);
    row.put("requestId", id);
    row.put("feature", "http");
    row.put("startedDateTime", started.toString());
    row.put("time", elapsed() / 1000000.0);
    Map<String, Object> request = message(true), response = message(false);
    row.put("request", request); row.put("response", response);
    Object requestData = request.get("postData"), responseData = response.get("content");
    if (requestData instanceof Map && ((Map<?, ?>) requestData).containsKey("encoding")) row.put("requestEncoding", "base64");
    if (((Map<?, ?>) responseData).containsKey("encoding")) row.put("encoding", "base64");
    row.put("cache", Collections.emptyMap());
    Map<String, Object> timings = new LinkedHashMap<>();
    for (String phase : new String[] {"blocked", "dns", "connect", "ssl", "send", "wait", "receive"}) timings.put(phase, -1);
    row.put("timings", timings);
    if (failure != null) row.put("error", failure);
    return row;
  }

  private long elapsed() { return finished ? elapsedNanos : System.nanoTime() - startedNanos; }

  private Map<String, Object> message(boolean request) {
    Map<String, Object> message = new LinkedHashMap<>();
    Map<String, List<String>> headers = request ? requestHeaders : responseHeaders;
    message.put("httpVersion", "");
    message.put("headers", headerRows(headers));
    message.put("cookies", Collections.emptyList());
    message.put("headersSize", -1);
    message.put("bodySize", request ? requestBytes : responseBytes);
    Map<String, Object> body = new LinkedHashMap<>();
    ByteArrayOutputStream bytes = request ? requestBody : responseBody;
    body.put("mimeType", header(headers, "Content-Type"));
    byte[] content = bytes.toByteArray();
    HttpBodies.content(body, content);
    body.put("truncated", (request ? requestBytes : responseBytes) > bytes.size());
    if (request) {
      message.put("method", method);
      message.put("url", url);
      message.put("queryString", Collections.emptyList());
      if (requestBytes > 0) message.put("postData", body);
    } else {
      message.put("status", status);
      message.put("statusText", "");
      message.put("redirectURL", header(headers, "Location"));
      body.put("size", responseBytes);
      message.put("content", body);
    }
    return message;
  }

  static Map<String, List<String>> redacted(Map<String, List<String>> headers) {
    Map<String, List<String>> result = new LinkedHashMap<>();
    for (Map.Entry<String, List<String>> entry : headers.entrySet()) {
      if (entry.getKey() == null) continue;
      String name = entry.getKey().toLowerCase(Locale.ROOT);
      boolean secret = name.equals("authorization") || name.equals("proxy-authorization") || name.equals("cookie") || name.equals("set-cookie") || name.equals("x-api-key");
      result.put(entry.getKey(), secret ? Collections.singletonList("[REDACTED]") : new ArrayList<>(entry.getValue()));
    }
    return result;
  }

  private static List<Map<String, String>> headerRows(Map<String, List<String>> headers) {
    List<Map<String, String>> rows = new ArrayList<>();
    for (Map.Entry<String, List<String>> header : headers.entrySet()) for (String value : header.getValue()) {
      Map<String, String> row = new LinkedHashMap<>(); row.put("name", header.getKey()); row.put("value", value); rows.add(row);
    }
    return rows;
  }

  private static String header(Map<String, List<String>> headers, String name) {
    for (Map.Entry<String, List<String>> entry : headers.entrySet()) if (name.equalsIgnoreCase(entry.getKey()) && !entry.getValue().isEmpty()) return entry.getValue().get(0);
    return "";
  }

  private static String sanitizedURL(String value) {
    if (value == null || value.isEmpty()) return "";
    URI uri = URI.create(value);
    String query = uri.getRawQuery();
    if (query != null) {
      String[] pairs = query.split("&", -1);
      for (int i = 0; i < pairs.length; i++) {
        String name = pairs[i].split("=", 2)[0].toLowerCase(Locale.ROOT);
        if (name.contains("token") || name.contains("password") || name.contains("secret") || name.equals("api_key")) pairs[i] = name + "=%5BREDACTED%5D";
      }
      query = String.join("&", pairs);
    }
    String authority = uri.getRawAuthority();
    if (authority != null && authority.contains("@")) authority = authority.substring(authority.lastIndexOf('@') + 1);
    return (uri.getScheme() == null ? "" : uri.getScheme() + ":") + (authority == null ? "" : "//" + authority) + uri.getRawPath() + (query == null ? "" : "?" + query);
  }
}
