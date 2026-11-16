package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import com.sun.net.httpserver.HttpServer;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import okhttp3.*;
import org.apache.http.client.methods.*;
import org.apache.http.impl.client.CloseableHttpClient;
import org.apache.http.impl.nio.client.CloseableHttpAsyncClient;
import org.apache.http.util.EntityUtils;
import org.junit.jupiter.api.Test;

class HttpLibraryAttachTest {
  @Test void capturesPreexistingApacheClassicAndAsyncClients() throws Exception {
    HttpServer server = server(); server.start();
    String base = "http://127.0.0.1:" + server.getAddress().getPort();
    CloseableHttpClient classic = org.apache.http.impl.client.HttpClients.createDefault();
    CloseableHttpAsyncClient async = org.apache.http.impl.nio.client.HttpAsyncClients.createDefault(); async.start();
    try (CloseableHttpResponse warm = classic.execute(new HttpGet(base + "/warm"))) { EntityUtils.consume(warm.getEntity()); }
    async.execute(new HttpGet(base + "/warm"), null).get(10, TimeUnit.SECONDS);
    HttpGet classicRequest = new HttpGet(base + "/classic"), asyncRequest = new HttpGet(base + "/async");
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "apache-http", Collections.<String, Object>singletonMap("bodies", "64KiB"));
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "apache-top", Collections.emptyMap())) {
      try (CloseableHttpResponse response = classic.execute(classicRequest)) { assertEquals("http-response", EntityUtils.toString(response.getEntity())); }
      org.apache.http.HttpResponse response = async.execute(asyncRequest, null).get(10, TimeUnit.SECONDS);
      assertEquals("http-response", EntityUtils.toString(response.getEntity()));
      List<Map<String, Object>> records = capture.drain();
      assertEquals(2, records.size(), "records=" + records + "; active=" + top.snapshot());
      for (Map<String, Object> record : records) assertEquals("http-response", ((Map<?, ?>) ((Map<?, ?>) record.get("response")).get("content")).get("text"));
      assertTrue(top.snapshot().isEmpty());
    } finally { classic.close(); async.close(); server.stop(0); }
  }

  @Test void capturesAnOkHttpClientCreatedBeforeAttachment() throws Exception {
    HttpServer server = server(); server.start();
    String base = "http://127.0.0.1:" + server.getAddress().getPort();
    OkHttpClient client = new OkHttpClient();
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "okhttp-http", Collections.<String, Object>singletonMap("bodies", "64KiB"));
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "okhttp-top", Collections.emptyMap())) {
      try (Response response = client.newCall(new Request.Builder().url(base + "/sync").build()).execute()) { assertEquals("http-response", response.body().string()); }
      CompletableFuture<String> result = new CompletableFuture<>();
      client.newCall(new Request.Builder().url(base + "/async").build()).enqueue(new Callback() {
        @Override public void onFailure(Call call, IOException error) { result.completeExceptionally(error); }
        @Override public void onResponse(Call call, Response response) throws IOException {
          try (Response received = response) { result.complete(received.body().string()); }
        }
      });
      assertEquals("http-response", result.get(10, TimeUnit.SECONDS));
      assertEquals(2, capture.drain().size());
      assertTrue(top.snapshot().isEmpty());
    } finally { client.dispatcher().executorService().shutdownNow(); client.connectionPool().evictAll(); server.stop(0); }
  }

  private static HttpServer server() throws IOException {
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", exchange -> {
      byte[] bytes = "http-response".getBytes(StandardCharsets.UTF_8);
      exchange.getResponseHeaders().set("Content-Type", "text/plain"); exchange.sendResponseHeaders(200, bytes.length);
      try (OutputStream body = exchange.getResponseBody()) { body.write(bytes); }
    });
    return server;
  }
}
