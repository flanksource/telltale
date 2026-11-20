package observerfixture;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.lang.management.ManagementFactory;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import javax.management.ObjectName;

public final class JmsOtelCompositionFixture {
  public static void main(String[] args) {
    try {
      execute(args);
      System.exit(0);
    } catch (Throwable error) {
      error.printStackTrace();
      System.exit(1);
    }
  }

  private static void execute(String[] args) throws Exception {
    Thread application = new Thread(() -> {
      try { Class.forName("Workload").getMethod("main", String[].class).invoke(null, (Object) args); }
      catch (Exception error) { throw new IllegalStateException("Demo application startup failed", error); }
    });
    application.start();
    String url = "http://127.0.0.1:" + args[0];
    long deadline = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(40);
    boolean ready = false;
    while (System.nanoTime() < deadline) {
      try { request(url + "/health", "GET"); ready = true; break; }
      catch (java.io.IOException notReady) { Thread.sleep(50); }
    }
    if (!ready) throw new IllegalStateException("Demo workload did not become ready");
    System.out.println("READY " + ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
    if (!"CAPTURE".equals(new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8)).readLine())) {
      throw new IllegalStateException("Expected CAPTURE after external attach");
    }
    for (String kind : Arrays.asList("http", "sql", "jms")) {
      Map<String, Object> params = new LinkedHashMap<>();
      if (kind.equals("http")) params.put("paths", Collections.singletonList("/demo/*"));
      if (kind.equals("sql")) params.put("connections", Collections.singletonList("*"));
      if (kind.equals("jms")) {
        params.put("direction", "both");
        params.put("destinations", Collections.singletonList("demo.orders"));
        params.put("body", true);
        params.put("maxBodyBytes", 4096);
      }
      Map<String, Object> capture = new LinkedHashMap<>();
      capture.put("kind", kind);
      capture.put("id", "composition-" + kind);
      capture.put("params", params);
      runtime("captureOpen", capture);
    }
    request(url + "/control/hold", "POST");
    request(url + "/control/release", "POST");
    long until = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(10);
    StringBuilder records = new StringBuilder();
    while (System.nanoTime() < until) {
      records.append(new String((byte[]) runtime("captureDump", Collections.singletonMap("id", "composition-jms")), StandardCharsets.UTF_8));
      if (records.indexOf("\"incoming\"") >= 0 && records.indexOf("\"outgoing\"") >= 0) break;
      Thread.sleep(20);
    }
    if (records.indexOf("\"incoming\"") < 0 || records.indexOf("\"outgoing\"") < 0) {
      throw new AssertionError("Missing completed JMS directions: " + records);
    }
    for (String kind : Arrays.asList("jms", "sql", "http")) runtime("captureClose", Collections.singletonMap("id", "composition-" + kind));
    System.out.println("RESULT " + records);
  }

  private static Object runtime(String operation, Map<String, Object> params) throws Exception {
    return ManagementFactory.getPlatformMBeanServer().invoke(
        new ObjectName("com.flanksource.telltale:type=Diagnostics"), operation,
        new Object[]{params}, new String[]{Map.class.getName()});
  }

  private static void request(String url, String method) throws Exception {
    HttpURLConnection request = (HttpURLConnection) new URL(url).openConnection();
    try {
      request.setRequestMethod(method);
      request.setConnectTimeout(1000);
      request.setReadTimeout(30000);
      if (request.getResponseCode() != 200) throw new java.io.IOException("HTTP " + request.getResponseCode() + " from " + url);
      try (java.io.InputStream body = request.getInputStream()) {
        byte[] buffer = new byte[1024];
        while (body.read(buffer) != -1) {}
      }
    } finally { request.disconnect(); }
  }
}
