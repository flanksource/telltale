package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import javax.servlet.AsyncContext;
import javax.servlet.http.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.apache.catalina.Context;
import org.apache.catalina.Wrapper;
import org.apache.catalina.startup.Tomcat;
import org.junit.jupiter.api.Test;

class ServletAttachTest {
  @Test void observesAnExistingAsyncServletUntilItsResponseCompletes() throws Exception {
    CountDownLatch release = new CountDownLatch(1), started = new CountDownLatch(1);
    ExecutorService workers = Executors.newSingleThreadExecutor();
    Path root = Paths.get("../../.tmp/servlet-http-tests"); Files.createDirectories(root);
    Tomcat tomcat = new Tomcat(); tomcat.setBaseDir(Files.createTempDirectory(root, "tomcat-").toString()); tomcat.setPort(0);
    Context context = tomcat.addContext("", root.toAbsolutePath().toString());
    HttpServlet servlet = new HttpServlet() {
      @Override protected void doGet(HttpServletRequest request, HttpServletResponse response) throws IOException {
        response.setContentType("text/plain");
        AsyncContext async = request.startAsync(); async.setTimeout(15000);
        workers.execute(() -> {
          try {
            response.getWriter().write("first"); response.getWriter().flush(); started.countDown();
            if (!release.await(15, TimeUnit.SECONDS)) throw new IllegalStateException("async fixture timed out");
            response.getWriter().write("second"); response.getWriter().close(); async.complete();
          } catch (Exception error) { async.complete(); throw new IllegalStateException(error); }
        });
      }
    };
    Wrapper wrapper = Tomcat.addServlet(context, "stream", servlet); wrapper.setAsyncSupported(true); context.addServletMappingDecoded("/stream", "stream");
    tomcat.getConnector(); tomcat.start();
    ObserverRuntime.install(ByteBuddyAgent.install());
    Map<String, Object> params = new LinkedHashMap<>(); params.put("direction", "incoming"); params.put("bodies", "64KiB");
    HttpURLConnection connection = (HttpURLConnection) new URL("http://127.0.0.1:" + tomcat.getConnector().getLocalPort() + "/stream").openConnection();
    connection.setReadTimeout(15000);
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "servlet-http", params);
         ObserverSubscription top = ObserverRuntime.subscribe("http_top", "servlet-top", params)) {
      try (InputStream body = connection.getInputStream()) {
        assertTrue(started.await(5, TimeUnit.SECONDS));
        assertTrue(capture.drain().isEmpty());
        assertEquals(1, top.snapshot().size());
        release.countDown();
        ByteArrayOutputStream bytes = new ByteArrayOutputStream(); byte[] buffer = new byte[128];
        for (int read; (read = body.read(buffer)) != -1;) bytes.write(buffer, 0, read);
        assertEquals("firstsecond", bytes.toString("UTF-8"));
      }
      long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
      while (!top.snapshot().isEmpty() && System.nanoTime() < deadline) Thread.yield();
      assertTrue(top.snapshot().isEmpty());
      List<Map<String, Object>> records = capture.drain();
      assertEquals(1, records.size());
      Map<?, ?> response = (Map<?, ?>) records.get(0).get("response");
      assertEquals(200, response.get("status"));
      assertEquals("firstsecond", ((Map<?, ?>) response.get("content")).get("text"));
    } finally { release.countDown(); workers.shutdownNow(); connection.disconnect(); tomcat.stop(); tomcat.destroy(); }
  }
}
