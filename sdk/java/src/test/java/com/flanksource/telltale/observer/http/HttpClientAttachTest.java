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
import java.util.concurrent.atomic.AtomicReference;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledForJreRange;
import org.junit.jupiter.api.condition.JRE;

@EnabledForJreRange(min = JRE.JAVA_11)
class HttpClientAttachTest {
  @Test void capturesExistingJavaHttpClientSyncAndAsyncWithOpenBodies() throws Exception {
    AtomicReference<CountDownLatch> release = new AtomicReference<>();
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", exchange -> {
      if (exchange.getRequestURI().getPath().equals("/warm")) { exchange.sendResponseHeaders(204, -1); exchange.close(); return; }
      exchange.sendResponseHeaders(200, 0);
      try (OutputStream body = exchange.getResponseBody()) {
        body.write("first".getBytes(StandardCharsets.UTF_8)); body.flush();
        try { if (!release.get().await(15, TimeUnit.SECONDS)) throw new IOException("HTTP client fixture timed out"); }
        catch (InterruptedException error) { Thread.currentThread().interrupt(); throw new IOException(error); }
        body.write("second".getBytes(StandardCharsets.UTF_8));
      }
    });
    server.start();
    String base = "http://127.0.0.1:" + server.getAddress().getPort();
    ClassLoader loader = ClassLoader.getSystemClassLoader();
    Object client = HttpAccess.callStatic(loader, "java.net.http.HttpClient", "newHttpClient");
    Object handler = HttpAccess.callStatic(loader, "java.net.http.HttpResponse$BodyHandlers", "ofInputStream");
    Object warm = request(loader, base + "/warm");
    Object warmed = HttpAccess.call(client, "send", warm, handler);
    ((InputStream) HttpAccess.call(warmed, "body")).close();
    Object[] requests = {request(loader, base + "/sync"), request(loader, base + "/async")};
    ObserverRuntime.install(ByteBuddyAgent.install());
    try {
      for (int attempt = 0; attempt < requests.length; attempt++) {
        release.set(new CountDownLatch(1));
        try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "java-http-" + attempt, Collections.<String, Object>singletonMap("bodies", "64KiB"));
             ObserverSubscription top = ObserverRuntime.subscribe("http_top", "java-top-" + attempt, Collections.emptyMap())) {
          Object response = attempt == 0 ? HttpAccess.call(client, "send", requests[attempt], handler)
              : ((CompletableFuture<?>) HttpAccess.call(client, "sendAsync", requests[attempt], handler)).get(10, TimeUnit.SECONDS);
          try (InputStream body = (InputStream) HttpAccess.call(response, "body")) {
            byte[] first = new byte[5]; assertEquals(5, body.read(first));
            assertEquals("first", new String(first, StandardCharsets.UTF_8));
            assertTrue(capture.drain().isEmpty(), "headers must not finish a Java HTTP client stream");
            List<Map<String, Object>> active = top.snapshot(); assertEquals(1, active.size(), active.toString());
            release.get().countDown();
            ByteArrayOutputStream rest = new ByteArrayOutputStream(); byte[] bytes = new byte[128];
            for (int count; (count = body.read(bytes)) >= 0;) rest.write(bytes, 0, count);
            assertEquals("second", rest.toString("UTF-8"));
          }
          List<Map<String, Object>> records = capture.drain(); assertEquals(1, records.size(), records.toString());
          assertEquals("firstsecond", ((Map<?, ?>) ((Map<?, ?>) records.get(0).get("response")).get("content")).get("text"));
          assertTrue(top.snapshot().isEmpty());
        }
      }
    } finally { if (release.get() != null) release.get().countDown(); server.stop(0); }
  }

  private static Object request(ClassLoader loader, String uri) {
    return HttpAccess.call(HttpAccess.callStatic(loader, "java.net.http.HttpRequest", "newBuilder", URI.create(uri)), "build");
  }
}
