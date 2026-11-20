package observerfixture.springhttp;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.apache.catalina.Context;
import org.apache.catalina.Wrapper;
import org.apache.catalina.startup.Tomcat;
import org.apache.catalina.webresources.TomcatURLStreamHandlerFactory;
import org.springframework.context.annotation.*;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.context.support.AnnotationConfigWebApplicationContext;
import org.springframework.web.context.request.async.DeferredResult;
import org.springframework.web.servlet.DispatcherServlet;
import org.springframework.web.servlet.ModelAndView;
import org.springframework.web.servlet.config.annotation.EnableWebMvc;

public final class SpringMvcFixture {
  public static void deferred() throws Exception {
    async(AsyncMode.DEFERRED);
  }

  public static void callable() throws Exception {
    async(AsyncMode.CALLABLE);
  }

  public static void forward() throws Exception {
    try (Fixture fixture = new Fixture()) {
      ObserverRuntime.install(ByteBuddyAgent.install());
      Map<String, Object> options = SpringHttpChecks.options();
      options.put("direction", "incoming");
      try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "spring-mvc-forward", options)) {
        assertEquals("warm", read(fixture.url("/forward/7")));
        Map<String, Object> row = SpringHttpChecks.completed(capture, 1).get(0);
        assertEquals("incoming", row.get("direction"));
        assertResponse(row, "warm");
        assertFalse(row.containsKey("error"));
      }
    }
  }

  private static void async(AsyncMode mode) throws Exception {
    try (Fixture fixture = new Fixture()) {
      assertEquals("warm", read(fixture.url("/warm")));
      ObserverRuntime.install(ByteBuddyAgent.install());
      Map<String, Object> options = SpringHttpChecks.options();
      options.put("direction", "incoming");
      try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "spring-mvc-async", options);
          ObserverSubscription top = ObserverRuntime.subscribe("http_top", "spring-mvc-async-top", options)) {
        Future<String> response = fixture.client.submit(() -> read(fixture.url(mode.path)));
        assertTrue(fixture.controller.accepted.await(5, TimeUnit.SECONDS));
        SpringHttpChecks.await(() -> top.snapshot().size() == 1);
        List<Map<String, Object>> active = top.snapshot();
        Map<String, Object> attributes = SpringHttpChecks.attributes(active.get(0));
        assertEquals(mode.route, attributes.get("route"));
        String handler = "AsyncController#" + (mode == AsyncMode.DEFERRED ? "async" : "callable");
        assertTrue("active MVC exchange must retain its matched method", String.valueOf(attributes.get("handler")).contains(handler));
        assertEquals("incoming", attributes.get("direction"));
        assertFalse("returning DeferredResult does not finish the exchange", response.isDone());
        assertTrue("the async exchange must stay active", capture.drain().isEmpty());
        Set<Object> ids = SpringHttpChecks.identities(active, "operationId");
        fixture.completion.submit(() -> {
          if (mode == AsyncMode.DEFERRED) fixture.controller.pending.setResult("accepted-7");
          else fixture.controller.release.countDown();
        }).get(5, TimeUnit.SECONDS);
        assertEquals("accepted-7", response.get(5, TimeUnit.SECONDS));
        List<Map<String, Object>> completed = SpringHttpChecks.completed(capture, 1);
        assertEquals(ids, SpringHttpChecks.identities(completed, "requestId"));
        assertEquals(mode.route, completed.get(0).get("route"));
        assertTrue("matched MVC handler must survive async redispatch", String.valueOf(completed.get(0).get("handler")).contains(handler));
        assertFalse(completed.get(0).containsKey("error"));
        assertResponse(completed.get(0), "accepted-7");
        SpringHttpChecks.await(() -> top.snapshot().isEmpty());
      }
    }
  }

  private enum AsyncMode {
    DEFERRED("/async/7", "/async/{id}"), CALLABLE("/callable/7", "/callable/{id}");
    final String path, route;
    AsyncMode(String path, String route) { this.path = path; this.route = route; }
  }

  @SuppressWarnings("unchecked")
  private static void assertResponse(Map<String, Object> row, String expected) {
    Map<String, Object> response = (Map<String, Object>) row.get("response");
    Map<String, Object> content = (Map<String, Object>) response.get("content");
    assertEquals(200, response.get("status"));
    assertEquals(expected, content.get("text"));
    assertEquals((long) expected.getBytes(StandardCharsets.UTF_8).length, ((Number) content.get("size")).longValue());
  }

  private static String read(String url) throws IOException {
    HttpURLConnection connection = (HttpURLConnection) new URL(url).openConnection();
    connection.setConnectTimeout(5000);
    connection.setReadTimeout(5000);
    try (InputStream input = connection.getInputStream(); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
      byte[] buffer = new byte[1024];
      int length;
      while ((length = input.read(buffer)) != -1) output.write(buffer, 0, length);
      return new String(output.toByteArray(), StandardCharsets.UTF_8);
    } finally { connection.disconnect(); }
  }

  @Configuration
  @EnableWebMvc
  public static class MvcConfiguration {}

  @RestController
  public static final class AsyncController {
    final CountDownLatch accepted = new CountDownLatch(1);
    final CountDownLatch release = new CountDownLatch(1);
    volatile DeferredResult<String> pending;

    @GetMapping("/warm") public String warm() { return "warm"; }

    @GetMapping("/async/{id}") public DeferredResult<String> async(@PathVariable("id") String id) {
      pending = new DeferredResult<>(10000L);
      accepted.countDown();
      return pending;
    }

    @GetMapping("/callable/{id}") public Callable<String> callable(@PathVariable("id") String id) {
      return () -> {
        accepted.countDown();
        if (!release.await(5, TimeUnit.SECONDS)) throw new IllegalStateException("the test did not release its MVC callable");
        return "accepted-" + id;
      };
    }

    @GetMapping("/forward/{id}") public ModelAndView forward() {
      return new ModelAndView("forward:/warm");
    }
  }

  private static final class Fixture implements AutoCloseable {
    final Tomcat tomcat = new Tomcat();
    final AnnotationConfigWebApplicationContext spring = new AnnotationConfigWebApplicationContext();
    final AsyncController controller = new AsyncController();
    final ExecutorService client = Executors.newSingleThreadExecutor();
    final ExecutorService completion = Executors.newSingleThreadExecutor();
    final Path directory;

    Fixture() throws Exception {
      directory = Files.createDirectories(Paths.get(".tmp", "spring-http", "mvc-" + UUID.randomUUID()));
      try {
        tomcat.setBaseDir(directory.toAbsolutePath().toString());
        tomcat.setPort(0);
        tomcat.getConnector();
        Context context = tomcat.addContext("", directory.toAbsolutePath().toString());
        java.lang.reflect.Method servletContext = Context.class.getMethod("getServletContext");
        Class<?> contextType = servletContext.getReturnType();
        // Isolated Jakarta runtimes serve HTTP without registering JVM-global URL protocols.
        if (contextType.getName().startsWith("jakarta.servlet.")) TomcatURLStreamHandlerFactory.disable();
        spring.getClass().getMethod("setServletContext", contextType).invoke(spring, servletContext.invoke(context));
        spring.register(MvcConfiguration.class);
        spring.addBeanFactoryPostProcessor(factory -> factory.registerSingleton("asyncController", controller));
        spring.refresh();
        String namespace = contextType.getName().substring(0, contextType.getName().indexOf(".servlet."));
        Class<?> servletType = Class.forName(namespace + ".servlet.Servlet", false, SpringMvcFixture.class.getClassLoader());
        Wrapper servlet = (Wrapper) Tomcat.class.getMethod("addServlet", Context.class, String.class, servletType)
            .invoke(null, context, "spring", new DispatcherServlet(spring));
        servlet.setAsyncSupported(true);
        servlet.setLoadOnStartup(1);
        context.addServletMappingDecoded("/", "spring");
        tomcat.start();
      } catch (Exception | Error failure) {
        try { close(); } catch (Exception cleanup) { failure.addSuppressed(cleanup); }
        throw failure;
      }
    }

    String url(String path) { return "http://127.0.0.1:" + tomcat.getConnector().getLocalPort() + path; }
    @Override public void close() throws Exception {
      client.shutdownNow();
      completion.shutdownNow();
      tomcat.stop();
      tomcat.destroy();
      spring.close();
      try (java.util.stream.Stream<Path> paths = Files.walk(directory)) {
        for (Path path : (Iterable<Path>) paths.sorted(Comparator.reverseOrder())::iterator) Files.delete(path);
      }
    }
  }
}
