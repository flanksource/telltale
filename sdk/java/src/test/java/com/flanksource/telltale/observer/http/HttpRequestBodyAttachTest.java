package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.*;
import com.sun.net.httpserver.HttpServer;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.jupiter.api.Test;

class HttpRequestBodyAttachTest {
  @Test void closingTheRequestBodyKeepsTheExchangeActiveAndCapturesItsBytesOnce() throws Exception {
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", exchange -> {
      byte[] bytes = read(exchange.getRequestBody());
      exchange.sendResponseHeaders(200, bytes.length);
      try (OutputStream body = exchange.getResponseBody()) { body.write(bytes); }
    });
    server.start();
    HttpURLConnection connection = (HttpURLConnection) new URL("http://127.0.0.1:" + server.getAddress().getPort() + "/echo").openConnection();
    connection.setRequestMethod("POST"); connection.setDoOutput(true);
    connection.setRequestProperty("Authorization", "Bearer private");
    byte[] payload = "application-request".getBytes(StandardCharsets.UTF_8);
    connection.setFixedLengthStreamingMode(payload.length);
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "request-body", Collections.<String, Object>singletonMap("bodies", "64KiB"));
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "request-top", Collections.emptyMap())) {
      try (OutputStream body = connection.getOutputStream()) { body.write(payload); }
      assertEquals(1, top.snapshot().size()); assertTrue(capture.drain().isEmpty());
      try (InputStream body = connection.getInputStream()) { assertArrayEquals(payload, read(body)); }
      List<Map<String, Object>> records = capture.drain(); assertEquals(1, records.size(), records.toString());
      Map<?, ?> request = (Map<?, ?>) records.get(0).get("request");
      assertEquals("application-request", ((Map<?, ?>) request.get("postData")).get("text"));
      assertFalse(request.toString().contains("Bearer private"));
      assertEquals("application-request", ((Map<?, ?>) ((Map<?, ?>) records.get(0).get("response")).get("content")).get("text"));
      assertTrue(top.snapshot().isEmpty());
    } finally { connection.disconnect(); server.stop(0); }
  }

  private static byte[] read(InputStream input) throws IOException {
    ByteArrayOutputStream output = new ByteArrayOutputStream(); byte[] bytes = new byte[128];
    for (int count; (count = input.read(bytes)) >= 0;) output.write(bytes, 0, count);
    return output.toByteArray();
  }
}
