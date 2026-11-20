package observerfixture.http;

import static org.junit.Assert.*;

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

public final class OkHttpVersionFixture {
  public static void run(String version) throws Exception {
    assertTrue(OkHttpClient.class.getProtectionDomain().getCodeSource().getLocation().toString().contains("okhttp-" + version));
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", exchange -> {
      byte[] bytes = "versioned-body".getBytes(StandardCharsets.UTF_8);
      exchange.sendResponseHeaders(200, bytes.length);
      try (OutputStream body = exchange.getResponseBody()) { body.write(bytes); }
    });
    server.start();
    String base = "http://127.0.0.1:" + server.getAddress().getPort();
    OkHttpClient client = new OkHttpClient();
    try (Response warm = client.newCall(new Request.Builder().url(base + "/warm").build()).execute()) { assertEquals("versioned-body", warm.body().string()); }
    Call sync = client.newCall(new Request.Builder().url(base + "/sync").build());
    Call async = client.newCall(new Request.Builder().url(base + "/async").build());
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "okhttp-" + version, Collections.<String, Object>singletonMap("bodies", "64KiB"));
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "okhttp-top-" + version, Collections.emptyMap())) {
      try (Response response = sync.execute()) { assertEquals("versioned-body", response.body().string()); }
      CompletableFuture<String> completed = new CompletableFuture<>();
      async.enqueue(new Callback() {
        @Override public void onFailure(Call call, IOException failure) { completed.completeExceptionally(failure); }
        @Override public void onResponse(Call call, Response response) throws IOException {
          try (Response received = response) { completed.complete(received.body().string()); }
        }
      });
      assertEquals("versioned-body", completed.get(10, TimeUnit.SECONDS));
      List<Map<String, Object>> rows = capture.drain();
      assertEquals(rows.toString(), 2, rows.size());
      for (Map<String, Object> row : rows) {
        Map<?, ?> content = (Map<?, ?>) ((Map<?, ?>) row.get("response")).get("content");
        assertEquals("versioned-body", content.get("text"));
        assertEquals(14L, content.get("size"));
      }
      assertTrue(top.snapshot().toString(), top.snapshot().isEmpty());
    } finally { client.dispatcher().executorService().shutdownNow(); client.connectionPool().evictAll(); server.stop(0); }
  }
}
