package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.*;
import com.sun.net.httpserver.*;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import net.bytebuddy.agent.ByteBuddyAgent;
import okhttp3.*;
import org.junit.jupiter.api.Test;

class HttpEdgeAttachTest {
  @Test void recordsEachRedirectHopWithItsOwnRequestIdentity() throws Exception {
    AtomicInteger requests = new AtomicInteger();
    HttpServer server = server(exchange -> {
      requests.incrementAndGet();
      if (exchange.getRequestURI().getPath().equals("/redirect")) {
        exchange.getResponseHeaders().set("Location", "/destination");
        respond(exchange, 302, "redirect-body");
      } else respond(exchange, 200, "destination-body");
    });
    HttpURLConnection connection = (HttpURLConnection) new URL(base(server) + "/redirect").openConnection();
    connection.setConnectTimeout(5000); connection.setReadTimeout(5000);
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = capture("redirect-hops"); ObserverSubscription top = top("redirect-top")) {
      try (InputStream body = connection.getInputStream()) { assertEquals("destination-body", read(body)); }
      assertEquals(2, requests.get());
      List<Map<String, Object>> rows = capture.drain();
      assertEquals(2, rows.size(), "both wire exchanges must be retained");
      assertEquals(Arrays.asList(302, 200), statuses(rows));
      assertNotEquals(rows.get(0).get("requestId"), rows.get(1).get("requestId"));
      assertTrue(top.snapshot().isEmpty());
    } finally { connection.disconnect(); server.stop(0); }
  }

  @Test void recordsRetryAttemptsWithoutChangingClientRetryBehavior() throws Exception {
    AtomicInteger requests = new AtomicInteger();
    HttpServer server = server(exchange -> {
      if (requests.incrementAndGet() == 1) {
        exchange.getResponseHeaders().set("Retry-After", "0");
        respond(exchange, 503, "retry-body");
      } else respond(exchange, 200, "retried-body");
    });
    OkHttpClient client = new OkHttpClient();
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = capture("retry-attempts"); ObserverSubscription top = top("retry-top")) {
      try (Response response = client.newCall(new Request.Builder().url(base(server) + "/retry").build()).execute()) {
        assertEquals(200, response.code()); assertEquals("retried-body", response.body().string());
      }
      assertEquals(2, requests.get(), "observer must preserve the client's one retry");
      List<Map<String, Object>> rows = capture.drain();
      assertEquals(2, rows.size(), "retry attempts must be separate wire exchanges");
      assertEquals(Arrays.asList(503, 200), statuses(rows));
      assertNotEquals(rows.get(0).get("requestId"), rows.get(1).get("requestId"));
      assertTrue(top.snapshot().isEmpty());
    } finally { close(client); server.stop(0); }
  }

  @Test void preservesTheClientCacheWithoutSendingAnotherRequest() throws Exception {
    AtomicInteger requests = new AtomicInteger();
    HttpServer server = server(exchange -> {
      requests.incrementAndGet(); exchange.getResponseHeaders().set("Cache-Control", "public, max-age=3600");
      respond(exchange, 200, "cached-body");
    });
    Path root = Paths.get(".tmp"); Files.createDirectories(root);
    Cache cache = new Cache(Files.createTempDirectory(root, "http-cache-").toFile(), 1 << 20);
    OkHttpClient client = new OkHttpClient.Builder().cache(cache).build();
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = capture("cache-requests")) {
      Request request = new Request.Builder().url(base(server) + "/cached").build();
      try (Response first = client.newCall(request).execute()) { assertEquals("cached-body", first.body().string()); }
      try (Response second = client.newCall(request).execute()) {
        assertNotNull(second.cacheResponse()); assertEquals("cached-body", second.body().string());
      }
      assertEquals(1, requests.get(), "observation must not bypass the application's cache");
      assertEquals(2, capture.drain().size(), "both logical calls remain visible including the cache hit");
    } finally { close(client); cache.delete(); server.stop(0); }
  }

  @Test void keepsAnUnreadResponseActiveUntilTheApplicationClosesIt() throws Exception {
    HttpServer server = server(exchange -> respond(exchange, 200, "unread-body"));
    OkHttpClient client = new OkHttpClient();
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = capture("unread-http"); ObserverSubscription top = top("unread-top")) {
      Response response = client.newCall(new Request.Builder().url(base(server) + "/unread").build()).execute();
      try {
        assertEquals(200, response.code());
        assertTrue(capture.drain().isEmpty(), "observer must not consume a response for the application");
        assertEquals(1, top.snapshot().size());
      } finally { response.close(); }
      List<Map<String, Object>> rows = capture.drain();
      assertEquals(1, rows.size()); assertEquals("", content(rows.get(0)).get("text"));
      assertTrue(top.snapshot().isEmpty(), "closing an unread response must retire the operation");
    } finally { close(client); server.stop(0); }
  }

  @Test void stoppingOneCapturePreservesItsSiblingAndActiveTopOperation() throws Exception {
    HttpServer server = server(exchange -> respond(exchange, 200, "shared-body"));
    HttpURLConnection connection = (HttpURLConnection) new URL(base(server) + "/shared").openConnection();
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription first = capture("shared-first"); ObserverSubscription second = capture("shared-second");
         ObserverSubscription top = top("shared-top")) {
      try (InputStream body = connection.getInputStream()) {
        assertEquals(1, top.snapshot().size());
        Object operation = top.snapshot().get(0).get("operationId");
        first.close();
        assertEquals(operation, top.snapshot().get(0).get("operationId"));
        assertTrue(ObserverRuntime.armed("http"));
        assertEquals("shared-body", read(body));
      }
      List<Map<String, Object>> firstRows = first.drain(), secondRows = second.drain();
      assertEquals(1, firstRows.size()); assertEquals(true, firstRows.get(0).get("_pending"));
      assertEquals(1, secondRows.size()); assertFalse(secondRows.get(0).containsKey("_pending"));
      assertEquals("shared-body", content(secondRows.get(0)).get("text"));
      assertTrue(top.snapshot().isEmpty());
    } finally { connection.disconnect(); server.stop(0); }
  }

  private static ObserverSubscription capture(String id) throws Exception {
    Map<String, Object> params = new LinkedHashMap<>(); params.put("direction", "outgoing"); params.put("bodies", "64KiB");
    return ObserverRuntime.subscribe("http", id, params);
  }
  private static ObserverSubscription top(String id) throws Exception {
    return ObserverRuntime.subscribe("http_top", id, Collections.<String, Object>singletonMap("direction", "outgoing"));
  }
  private static HttpServer server(HttpHandler handler) throws IOException {
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", handler); server.start(); return server;
  }
  private static String base(HttpServer server) { return "http://127.0.0.1:" + server.getAddress().getPort(); }
  private static void respond(HttpExchange exchange, int status, String text) throws IOException {
    byte[] bytes = text.getBytes(StandardCharsets.UTF_8);
    exchange.getResponseHeaders().set("Content-Type", "text/plain"); exchange.sendResponseHeaders(status, bytes.length);
    try (OutputStream output = exchange.getResponseBody()) { output.write(bytes); }
    finally { exchange.close(); }
  }
  private static String read(InputStream input) throws IOException {
    ByteArrayOutputStream output = new ByteArrayOutputStream(); byte[] bytes = new byte[128];
    for (int count; (count = input.read(bytes)) != -1;) output.write(bytes, 0, count);
    return output.toString("UTF-8");
  }
  private static List<Integer> statuses(List<Map<String, Object>> rows) {
    List<Integer> statuses = new ArrayList<>();
    for (Map<String, Object> row : rows) statuses.add(((Number) ((Map<?, ?>) row.get("response")).get("status")).intValue());
    return statuses;
  }
  private static Map<?, ?> content(Map<String, Object> row) { return (Map<?, ?>) ((Map<?, ?>) row.get("response")).get("content"); }
  private static void close(OkHttpClient client) { client.dispatcher().executorService().shutdownNow(); client.connectionPool().evictAll(); }
}
