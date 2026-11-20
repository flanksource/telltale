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
import org.junit.jupiter.api.Test;

class HttpAttachTest {
  @Test void attachesToAnExistingJdkConnectionAndKeepsStreamingBodyActive() throws Exception {
    CountDownLatch release = new CountDownLatch(1);
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/stream", exchange -> {
      exchange.getResponseHeaders().add("Content-Type", "text/plain");
      exchange.sendResponseHeaders(200, 0);
      try (OutputStream body = exchange.getResponseBody()) {
        body.write("first".getBytes(StandardCharsets.UTF_8)); body.flush();
        try { if (!release.await(15, TimeUnit.SECONDS)) throw new IOException("stream fixture timed out"); }
        catch (InterruptedException error) { Thread.currentThread().interrupt(); throw new IOException(error); }
        body.write("second".getBytes(StandardCharsets.UTF_8));
      }
    });
    server.start();
    URL url = new URL("http://127.0.0.1:" + server.getAddress().getPort() + "/stream");
    HttpURLConnection connection = (HttpURLConnection) url.openConnection();
    connection.setConnectTimeout(5000); connection.setReadTimeout(15000);
    ObserverRuntime.install(ByteBuddyAgent.install());
    Map<String, Object> params = new LinkedHashMap<>(); params.put("direction", "outgoing"); params.put("bodies", "64KiB");
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "jdk-http", params);
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "jdk-top", params)) {
      try (InputStream body = connection.getInputStream()) {
        byte[] first = new byte[5];
        int count = body.read(first);
        assertEquals("first", new String(first, 0, count, StandardCharsets.UTF_8));
        assertTrue(capture.drain().isEmpty(), "headers must not finish a streaming response");
        List<Map<String, Object>> active = top.snapshot();
        assertEquals(1, active.size());
        assertEquals(url.toString(), ((Map<?, ?>) active.get(0).get("attributes")).get("url"));
        release.countDown();
        ByteArrayOutputStream rest = new ByteArrayOutputStream(); byte[] buffer = new byte[128];
        for (int read; (read = body.read(buffer)) != -1;) rest.write(buffer, 0, read);
        assertEquals("second", rest.toString("UTF-8"));
      }
      List<Map<String, Object>> records = capture.drain();
      assertEquals(1, records.size());
      Map<?, ?> response = (Map<?, ?>) records.get(0).get("response");
      assertEquals(200, response.get("status"));
      assertEquals("firstsecond", ((Map<?, ?>) response.get("content")).get("text"));
      assertTrue(top.snapshot().isEmpty(), "completed requests must disappear from top");
    } finally { release.countDown(); connection.disconnect(); server.stop(0); }
  }

  @Test void isolatesSubscriberBodyCapsWithoutChangingTheResponse() throws Exception {
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/body", exchange -> {
      byte[] bytes = "public-body".getBytes(StandardCharsets.UTF_8);
      exchange.sendResponseHeaders(200, bytes.length);
      try (OutputStream body = exchange.getResponseBody()) { body.write(bytes); }
    });
    server.start();
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription metadata = ObserverRuntime.subscribe("http", "metadata-http", Collections.<String, Object>singletonMap("bodies", "0"));
         ObserverSubscription full = ObserverRuntime.subscribe("http", "full-http", Collections.<String, Object>singletonMap("bodies", "64KiB"))) {
      HttpURLConnection connection = (HttpURLConnection) new URL("http://127.0.0.1:" + server.getAddress().getPort() + "/body").openConnection();
      try (InputStream body = connection.getInputStream()) {
        ByteArrayOutputStream received = new ByteArrayOutputStream(); byte[] buffer = new byte[128];
        for (int read; (read = body.read(buffer)) != -1;) received.write(buffer, 0, read);
        assertEquals("public-body", received.toString("UTF-8"));
      }
      assertEquals("", content(metadata.drain().get(0)).get("text"));
      Map<String, Object> retained = full.drain().get(0);
      assertEquals("public-body", content(retained).get("text"), retained.toString());
    } finally { server.stop(0); }
  }

  private static Map<?, ?> content(Map<String, Object> row) { return (Map<?, ?>) ((Map<?, ?>) row.get("response")).get("content"); }
}
