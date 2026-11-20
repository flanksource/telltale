package observerfixture.springhttp;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.reactivestreams.Subscription;
import org.springframework.context.annotation.*;
import org.springframework.http.MediaType;
import org.springframework.http.client.reactive.ReactorClientHttpConnector;
import org.springframework.http.server.reactive.ReactorHttpHandlerAdapter;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.reactive.config.EnableWebFlux;
import org.springframework.web.reactive.function.client.WebClient;
import org.springframework.web.reactive.function.server.RouterFunction;
import org.springframework.web.reactive.function.server.RouterFunctions;
import org.springframework.web.reactive.function.server.RequestPredicates;
import org.springframework.web.reactive.function.server.ServerResponse;
import org.springframework.web.server.adapter.WebHttpHandlerBuilder;
import reactor.core.publisher.*;
import reactor.core.scheduler.Scheduler;
import reactor.core.scheduler.Schedulers;
import reactor.netty.DisposableServer;
import reactor.netty.http.client.HttpClient;
import reactor.netty.http.server.HttpServer;
import reactor.netty.resources.ConnectionProvider;
import reactor.netty.resources.LoopResources;

public final class SpringReactiveFixture {
  public static void streaming() throws Exception { verifyStreaming("/stream/7", "/stream/{id}"); }
  public static void functionalStreaming() throws Exception { verifyStreaming("/functional/7", "/functional/{id}"); }

  private static void verifyStreaming(String path, String route) throws Exception {
    try (Fixture fixture = new Fixture()) {
      WebClient existing = fixture.context.getBean(WebClient.class);
      assertEquals("warm", existing.get().uri(fixture.url("/warm")).retrieve().bodyToMono(String.class).block(Duration.ofSeconds(5)));
      Flux<String> body = existing.get().uri(fixture.url(path)).retrieve().bodyToFlux(String.class);
      assertEquals("assembling a request must not execute it", 0, fixture.controller.requests.get());
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "spring-stream", SpringHttpChecks.options());
          ObserverSubscription top = ObserverRuntime.subscribe("http_top", "spring-stream-top", SpringHttpChecks.options())) {
        assertTrue("a cold publisher has no active request", top.snapshot().isEmpty());
        assertEquals("arming must not subscribe to an assembled request", 0, fixture.controller.requests.get());
        fixture.consume(body);
        fixture.awaitAccepted();
        fixture.controller.emit("first");
        fixture.consumer.awaitFirst();
        assertTrue("consumer stays on its application scheduler", fixture.consumer.thread.startsWith("spring-http-consumer"));
        List<Map<String, Object>> active = activePair(top, route);
        Set<Object> ids = SpringHttpChecks.identities(active, "operationId");
        assertTrue("headers and an open body must not emit completed exchanges", capture.drain().isEmpty());
        fixture.controller.emit("second");
        assertFalse("observation must preserve downstream demand", fixture.consumer.second.await(150, TimeUnit.MILLISECONDS));
        assertEquals(Collections.singletonList("first"), fixture.consumer.values);
        fixture.consumer.request(1);
        assertTrue(fixture.consumer.second.await(5, TimeUnit.SECONDS));
        fixture.controller.complete();
        fixture.consumer.request(1);
        assertTrue(fixture.consumer.finished.await(5, TimeUnit.SECONDS));
        assertNull(fixture.consumer.failure);
        assertEquals(Arrays.asList("first", "second"), fixture.consumer.values);
        List<Map<String, Object>> completed = SpringHttpChecks.completed(capture, 2);
        assertEquals(ids, SpringHttpChecks.identities(completed, "requestId"));
        assertEquals("observation must not add a body subscription", 1, fixture.controller.subscriptions.get());
        for (Map<String, Object> row : completed) {
          assertBody(row, "data:first\n\ndata:second\n\n");
          if (route.equals("/stream/{id}") && "incoming".equals(row.get("direction")))
            assertTrue("annotated handler must survive body completion", String.valueOf(row.get("handler")).contains("StreamingController#stream"));
        }
        SpringHttpChecks.await(() -> top.snapshot().isEmpty());
        assertTrue("completion must not produce a duplicate record", capture.drain().isEmpty());
      }
    }
  }

  public static void cancellation() throws Exception {
    try (Fixture fixture = new Fixture()) {
      WebClient existing = fixture.context.getBean(WebClient.class);
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("http", "spring-cancel", SpringHttpChecks.options());
          ObserverSubscription top = ObserverRuntime.subscribe("http_top", "spring-cancel-top", SpringHttpChecks.options())) {
        fixture.consume(existing.get().uri(fixture.url("/stream/8")).retrieve().bodyToFlux(String.class));
        fixture.awaitAccepted();
        fixture.controller.emit("first");
        fixture.consumer.awaitFirst();
        Set<Object> ids = SpringHttpChecks.identities(activePair(top, "/stream/{id}"), "operationId");
        fixture.consumer.cancel();
        assertTrue("cancellation must reach the server publisher", fixture.controller.cancelled.await(5, TimeUnit.SECONDS));
        List<Map<String, Object>> completed = SpringHttpChecks.completed(capture, 2);
        assertEquals(ids, SpringHttpChecks.identities(completed, "requestId"));
        Map<String, Object> outgoing = completed.stream().filter(row -> "outgoing".equals(row.get("direction"))).findFirst().get();
        assertTrue("cancellation must not be reported as success: " + outgoing, String.valueOf(outgoing.get("error")).toLowerCase(Locale.ROOT).contains("cancel"));
        SpringHttpChecks.await(() -> top.snapshot().isEmpty());
      }
    }
  }

  private static List<Map<String, Object>> activePair(ObserverSubscription top, String route) throws Exception {
    SpringHttpChecks.await(() -> top.snapshot().size() == 2);
    List<Map<String, Object>> active = top.snapshot();
    Set<Object> directions = new HashSet<>();
    for (Map<String, Object> row : active) {
      Map<String, Object> attributes = SpringHttpChecks.attributes(row);
      directions.add(attributes.get("direction"));
      assertTrue(((Number) row.get("elapsedNs")).longValue() > 0);
      assertEquals("GET", attributes.get("method"));
      if ("incoming".equals(attributes.get("direction"))) {
        assertEquals(route, attributes.get("route"));
        if (route.equals("/stream/{id}"))
          assertTrue("active annotated exchange must retain its matched method", String.valueOf(attributes.get("handler")).contains("StreamingController#stream"));
      } else assertEquals("response headers do not complete the operation", "response", attributes.get("phase"));
    }
    assertEquals(new HashSet<>(Arrays.asList("incoming", "outgoing")), directions);
    return active;
  }

  @SuppressWarnings("unchecked")
  private static void assertBody(Map<String, Object> row, String expected) {
    assertFalse("normal completion must preserve success: " + row, row.containsKey("error"));
    Map<String, Object> response = (Map<String, Object>) row.get("response");
    Map<String, Object> content = (Map<String, Object>) response.get("content");
    assertEquals(200, response.get("status"));
    assertEquals("body bytes must be observed once", expected, content.get("text"));
    assertEquals((long) expected.getBytes(StandardCharsets.UTF_8).length, ((Number) content.get("size")).longValue());
    assertEquals(false, content.get("truncated"));
  }

  @Configuration
  @EnableWebFlux
  public static class ReactiveConfiguration {
    @Bean public RouterFunction<ServerResponse> functionalRoute(StreamingController controller) {
      return RouterFunctions.route(RequestPredicates.GET("/functional/{id}"), request -> ServerResponse.ok()
          .contentType(MediaType.TEXT_EVENT_STREAM).body(controller.stream(request.pathVariable("id")), String.class));
    }
  }

  @RestController
  public static final class StreamingController {
    final Sinks.Many<String> body = Sinks.many().unicast().onBackpressureBuffer();
    final AtomicInteger requests = new AtomicInteger();
    final AtomicInteger subscriptions = new AtomicInteger();
    final CountDownLatch accepted = new CountDownLatch(1);
    final CountDownLatch cancelled = new CountDownLatch(1);

    @GetMapping("/warm") public String warm() { return "warm"; }

    @GetMapping(value = "/stream/{id}", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public Flux<String> stream(@PathVariable("id") String id) {
      requests.incrementAndGet();
      accepted.countDown();
      return body.asFlux().doOnSubscribe(ignored -> subscriptions.incrementAndGet()).doOnCancel(cancelled::countDown);
    }

    void emit(String value) { assertEquals(Sinks.EmitResult.OK, body.tryEmitNext(value)); }
    void complete() { assertEquals(Sinks.EmitResult.OK, body.tryEmitComplete()); }
  }

  private static final class ControlledSubscriber extends BaseSubscriber<String> {
    final List<String> values = new CopyOnWriteArrayList<>();
    final CountDownLatch first = new CountDownLatch(1);
    final CountDownLatch second = new CountDownLatch(1);
    final CountDownLatch finished = new CountDownLatch(1);
    volatile String thread;
    volatile Throwable failure;

    @Override protected void hookOnSubscribe(Subscription subscription) { request(1); }
    @Override protected void hookOnNext(String value) {
      thread = Thread.currentThread().getName();
      values.add(value);
      if (values.size() == 1) first.countDown(); else second.countDown();
    }
    @Override protected void hookOnError(Throwable error) { failure = error; finished.countDown(); }
    @Override protected void hookOnComplete() { finished.countDown(); }

    void awaitFirst() throws Exception {
      boolean received = first.await(5, TimeUnit.SECONDS);
      if (failure != null) throw new AssertionError("the real Spring HTTP pipeline failed", failure);
      assertTrue("first SSE event must reach the application", received);
    }
  }

  static final class Fixture implements AutoCloseable {
    final AnnotationConfigApplicationContext context = new AnnotationConfigApplicationContext();
    final StreamingController controller = new StreamingController();
    final ControlledSubscriber consumer = new ControlledSubscriber();
    final Scheduler scheduler = Schedulers.newSingle("spring-http-consumer");
    final ConnectionProvider pool = ConnectionProvider.create("spring-http-" + UUID.randomUUID(), 1);
    final LoopResources loops = LoopResources.create("spring-http-io", 1, true);
    final DisposableServer server;

    Fixture() {
      context.registerBean("streamingController", StreamingController.class, () -> controller);
      context.registerBean("existingWebClient", WebClient.class, () -> WebClient.builder()
          .clientConnector(new ReactorClientHttpConnector(HttpClient.create(pool).runOn(loops))).build());
      context.register(ReactiveConfiguration.class);
      context.refresh();
      ReactorHttpHandlerAdapter handler = new ReactorHttpHandlerAdapter(WebHttpHandlerBuilder.applicationContext(context).build());
      server = HttpServer.create().runOn(loops).host("127.0.0.1").port(0).handle(handler).bindNow(Duration.ofSeconds(5));
      context.getBean(WebClient.class);
    }

    String url(String path) { return "http://127.0.0.1:" + server.port() + path; }
    void consume(Flux<String> body) { body.publishOn(scheduler, 1).subscribe(consumer); }
    void awaitAccepted() throws Exception {
      boolean accepted = controller.accepted.await(5, TimeUnit.SECONDS);
      if (consumer.failure != null) throw new AssertionError("the real Spring HTTP request failed before its handler", consumer.failure);
      assertTrue("the real Spring handler must receive the subscribed request", accepted);
    }
    @Override public void close() {
      consumer.cancel();
      server.disposeNow(Duration.ofSeconds(5));
      context.close();
      scheduler.dispose();
      pool.disposeLater().block(Duration.ofSeconds(5));
      loops.disposeLater().block(Duration.ofSeconds(5));
    }
  }
}
