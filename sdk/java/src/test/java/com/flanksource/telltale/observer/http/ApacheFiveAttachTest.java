package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import com.sun.net.httpserver.HttpServer;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.TimeUnit;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.apache.hc.client5.http.async.methods.SimpleHttpResponse;
import org.apache.hc.client5.http.async.methods.SimpleHttpRequests;
import org.apache.hc.client5.http.classic.methods.HttpGet;
import org.apache.hc.client5.http.impl.async.CloseableHttpAsyncClient;
import org.apache.hc.client5.http.impl.classic.CloseableHttpClient;
import org.apache.hc.core5.http.io.entity.EntityUtils;
import org.junit.jupiter.api.Test;

class ApacheFiveAttachTest {
  @Test void capturesExistingApacheFiveClassicAndAsyncClients() throws Exception {
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", exchange -> {
      byte[] body = "apache-five".getBytes(StandardCharsets.UTF_8); exchange.sendResponseHeaders(200, body.length);
      try (OutputStream output = exchange.getResponseBody()) { output.write(body); }
    });
    server.start();
    String base = "http://127.0.0.1:" + server.getAddress().getPort();
    CloseableHttpClient classic = org.apache.hc.client5.http.impl.classic.HttpClients.createDefault();
    CloseableHttpAsyncClient async = org.apache.hc.client5.http.impl.async.HttpAsyncClients.createDefault(); async.start();
    classic.execute(new HttpGet(base + "/warm"), response -> EntityUtils.toString(response.getEntity()));
    async.execute(SimpleHttpRequests.get(base + "/warm"), null).get(10, TimeUnit.SECONDS);
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "apache-five", Collections.<String, Object>singletonMap("bodies", "64KiB"));
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "apache-five-top", Collections.emptyMap())) {
      assertEquals("apache-five", classic.execute(new HttpGet(base + "/classic"), response -> EntityUtils.toString(response.getEntity())));
      SimpleHttpResponse response = async.execute(SimpleHttpRequests.get(base + "/async"), null).get(10, TimeUnit.SECONDS);
      assertEquals("apache-five", response.getBodyText());
      List<Map<String, Object>> records = capture.drain();
      assertEquals(2, records.size(), "records=" + records + "; active=" + top.snapshot());
      for (Map<String, Object> record : records) assertEquals("apache-five", ((Map<?, ?>) ((Map<?, ?>) record.get("response")).get("content")).get("text"));
      assertTrue(top.snapshot().isEmpty());
    } finally { classic.close(); async.close(); server.stop(0); }
  }
}
