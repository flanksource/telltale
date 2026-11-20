import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import io.opentelemetry.api.trace.Span;

final class DemoCycle {
  private final CountDownLatch release = new CountDownLatch(1);
  private final CountDownLatch entered = new CountDownLatch(5);
  private final CountDownLatch messageFinished = new CountDownLatch(1);
  private final AtomicInteger httpEntered = new AtomicInteger();
  private final AtomicBoolean jdbcEntered = new AtomicBoolean();
  private final AtomicBoolean r2dbcEntered = new AtomicBoolean();
  private final AtomicBoolean jmsEntered = new AtomicBoolean();
  final String id;
  final Span span;
  CompletableFuture<Void> work;

  DemoCycle(String id, boolean held, Span span) {
    this.id = id;
    this.span = span;
    if (!held) release.countDown();
  }

  void http() throws InterruptedException {
    httpEntered.incrementAndGet();
    entered.countDown();
    awaitRelease();
  }

  int sql(String api) throws InterruptedException {
    AtomicBoolean marker;
    if ("jdbc".equals(api)) marker = jdbcEntered;
    else if ("r2dbc".equals(api)) marker = r2dbcEntered;
    else throw new IllegalArgumentException("Unknown demo SQL API: " + api);
    if (!marker.compareAndSet(false, true)) throw new IllegalStateException("SQL API entered twice: " + api);
    entered.countDown();
    awaitRelease();
    return 7;
  }

  void message() throws InterruptedException {
    if (!jmsEntered.compareAndSet(false, true)) throw new IllegalStateException("Demo message entered twice");
    entered.countDown();
    awaitRelease();
    messageFinished.countDown();
  }

  void awaitEntered() throws InterruptedException {
    if (!entered.await(20, TimeUnit.SECONDS)) throw new IllegalStateException("Demo operations did not enter: " + state());
  }

  void release() {
    release.countDown();
  }

  void awaitFinished() throws Exception {
    work.get(20, TimeUnit.SECONDS);
    if (!messageFinished.await(20, TimeUnit.SECONDS)) throw new IllegalStateException("Demo JMS processing did not complete");
    span.end();
  }

  Map<String, Object> state() {
    Map<String, Object> values = new LinkedHashMap<>();
    values.put("cycle", id);
    values.put("holding", release.getCount() != 0);
    values.put("httpEntered", httpEntered.get());
    values.put("jdbcEntered", jdbcEntered.get());
    values.put("r2dbcEntered", r2dbcEntered.get());
    values.put("jmsEntered", jmsEntered.get());
    return values;
  }

  private void awaitRelease() throws InterruptedException {
    if (!release.await(Duration.ofMinutes(2).toMillis(), TimeUnit.MILLISECONDS)) {
      throw new IllegalStateException("Demo cycle was not released before its deadline: " + id);
    }
  }
}
