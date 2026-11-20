import io.netty.channel.ChannelOption;
import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.context.Context;
import io.opentelemetry.context.Scope;
import io.r2dbc.spi.ConnectionFactories;
import io.r2dbc.spi.ConnectionFactory;
import java.nio.file.Path;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.concurrent.atomic.AtomicBoolean;
import javax.jms.Message;
import org.apache.activemq.artemis.api.core.TransportConfiguration;
import org.apache.activemq.artemis.core.config.impl.ConfigurationImpl;
import org.apache.activemq.artemis.core.remoting.impl.invm.InVMAcceptorFactory;
import org.apache.activemq.artemis.core.server.embedded.EmbeddedActiveMQ;
import org.apache.activemq.artemis.jms.client.ActiveMQConnectionFactory;
import org.h2.jdbcx.JdbcDataSource;
import org.springframework.http.client.SimpleClientHttpRequestFactory;
import org.springframework.http.client.reactive.ReactorClientHttpConnector;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jms.core.JmsTemplate;
import org.springframework.jms.listener.DefaultMessageListenerContainer;
import org.springframework.jms.listener.SessionAwareMessageListener;
import org.springframework.r2dbc.core.DatabaseClient;
import org.springframework.web.client.RestTemplate;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.netty.http.client.HttpClient;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

final class DemoOperations implements AutoCloseable {
  private static final Duration TIMEOUT = Duration.ofSeconds(30);
  private final AtomicReference<Throwable> failure = new AtomicReference<>();
  private final AtomicBoolean closed = new AtomicBoolean();
  private final AtomicLong sequences = new AtomicLong();
  private final AtomicLong completed = new AtomicLong();
  private final ExecutorService workers = Executors.newCachedThreadPool();
  private final ScheduledExecutorService traffic = Executors.newSingleThreadScheduledExecutor();
  private final EmbeddedActiveMQ broker = new EmbeddedActiveMQ();
  private final ActiveMQConnectionFactory factory = new ActiveMQConnectionFactory("vm://0");
  private final DefaultMessageListenerContainer listener = new DefaultMessageListenerContainer();
  private final JmsTemplate jms;
  private final JdbcTemplate jdbc;
  private final DatabaseClient r2dbc;
  private final RestTemplate rest;
  private final WebClient web;
  private volatile DemoCycle active;
  private volatile DemoCycle last;
  private volatile boolean ready;
  private volatile boolean trafficEnabled;
  private String baseUrl;

  DemoOperations(Path directory) throws Exception {
    try {
      JdbcDataSource dataSource = new JdbcDataSource();
      dataSource.setURL("jdbc:h2:mem:demo_jdbc;DB_CLOSE_DELAY=-1");
      jdbc = new JdbcTemplate(dataSource);
      jdbc.execute("CREATE ALIAS DEMO_HOLD FOR 'Workload.hold'");
      ConnectionFactory connections = ConnectionFactories.get("r2dbc:h2:mem:///demo_r2dbc;DB_CLOSE_DELAY=-1");
      Mono.usingWhen(Mono.from(connections.create()),
          connection -> Flux.from(connection.createStatement("CREATE ALIAS DEMO_HOLD FOR 'Workload.hold'").execute())
              .flatMap(result -> result.getRowsUpdated()).then(),
          connection -> Mono.from(connection.close())).block(TIMEOUT);
      r2dbc = DatabaseClient.create(connections);
      SimpleClientHttpRequestFactory requests = new SimpleClientHttpRequestFactory();
      requests.setConnectTimeout(5000);
      requests.setReadTimeout(120000);
      rest = new RestTemplate(requests);
      HttpClient client = HttpClient.create().option(ChannelOption.CONNECT_TIMEOUT_MILLIS, 5000).responseTimeout(Duration.ofMinutes(2));
      web = WebClient.builder().clientConnector(new ReactorClientHttpConnector(client)).build();
      broker.setConfiguration(new ConfigurationImpl().setPersistenceEnabled(false).setSecurityEnabled(false)
          .setBindingsDirectory(directory.resolve("artemis/bindings").toString())
          .setJournalDirectory(directory.resolve("artemis/journal").toString())
          .setPagingDirectory(directory.resolve("artemis/paging").toString())
          .setLargeMessagesDirectory(directory.resolve("artemis/large-messages").toString())
          .addAcceptorConfiguration(new TransportConfiguration(InVMAcceptorFactory.class.getName())));
      broker.start();
      jms = new JmsTemplate(factory);
      listener.setConnectionFactory(factory);
      listener.setDestinationName("demo.orders");
      listener.setSessionTransacted(true);
      listener.setErrorHandler(this::recordFailure);
      listener.setMessageListener((SessionAwareMessageListener<Message>) (message, session) -> {
        DemoCycle cycle = requireCycle(message.getStringProperty("cycle"));
        try { cycle.message(); }
        catch (InterruptedException interrupted) {
          Thread.currentThread().interrupt();
          throw new IllegalStateException("Demo JMS processing interrupted", interrupted);
        }
      });
      listener.afterPropertiesSet();
      listener.start();
    } catch (Exception | Error error) {
      try { close(); } catch (Exception cleanup) { error.addSuppressed(cleanup); }
      throw error;
    }
  }

  void start(String url) throws Exception {
    baseUrl = url;
    DemoCycle warm = startCycle(false);
    warm.awaitEntered();
    finish(warm);
    ready = true;
    traffic.scheduleWithFixedDelay(this::runTraffic, 1, 2, TimeUnit.SECONDS);
  }

  synchronized Map<String, Object> begin() throws Exception {
    checkHealthy();
    if (trafficEnabled) throw new IllegalStateException("Stop interactive traffic before holding a deterministic cycle");
    DemoCycle cycle = startCycle(true);
    cycle.awaitEntered();
    return state();
  }

  synchronized Map<String, Object> release() throws Exception {
    checkHealthy();
    if (active == null) throw new IllegalStateException("There is no active demo cycle to release");
    finish(active);
    return state();
  }

  Map<String, Object> traffic(boolean enabled) throws Exception {
    synchronized (this) {
      checkHealthy();
      if (enabled && active != null) throw new IllegalStateException("Release the held demo cycle before starting interactive traffic");
      trafficEnabled = enabled;
    }
    if (!enabled) {
      long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(30);
      while (active != null) {
        checkHealthy();
        if (System.nanoTime() > deadline) throw new IllegalStateException("Interactive demo traffic did not stop");
        Thread.sleep(10);
      }
    }
    return state();
  }

  void holdHttp(String id) throws InterruptedException {
    int separator = id.indexOf('-');
    if (separator < 0) throw new IllegalArgumentException("Demo request requires a client and cycle ID: " + id);
    requireCycle(id.substring(separator + 1)).http();
  }

  int holdSql(String api) throws InterruptedException {
    DemoCycle cycle = active;
    if (cycle == null) throw new IllegalStateException("SQL started outside a demo cycle");
    return cycle.sql(api);
  }

  boolean healthy() {
    return ready && failure.get() == null;
  }

  Map<String, Object> state() {
    DemoCycle cycle = active;
    if (cycle == null) cycle = last;
    Map<String, Object> values = cycle == null ? new LinkedHashMap<>() : cycle.state();
    values.put("ready", ready);
    values.put("trafficEnabled", trafficEnabled);
    values.put("completedCycles", completed.get());
    values.put("failure", failure.get() == null ? null : failure.get().toString());
    return values;
  }

  private DemoCycle startCycle(boolean held) {
    if (active != null) throw new IllegalStateException("Demo cycle already active: " + active.id);
    DemoCycle cycle = new DemoCycle(Long.toString(sequences.incrementAndGet()), held,
        GlobalOpenTelemetry.getTracer("jvm-demo").spanBuilder("jvm-demo-cycle").startSpan());
    active = cycle;
    try (Scope ignored = cycle.span.makeCurrent()) {
      cycle.work = CompletableFuture.allOf(
        task(() -> expect(rest.getForObject(baseUrl + "/demo/rest-" + cycle.id, String.class), "demo-rest-" + cycle.id)),
        task(() -> expect(web.get().uri(baseUrl + "/demo/web-" + cycle.id).retrieve().bodyToMono(String.class).block(Duration.ofMinutes(2)), "demo-web-" + cycle.id)),
        task(() -> expect(jdbc.queryForObject("SELECT DEMO_HOLD('jdbc') AS demo_value", Integer.class), 7)),
        task(() -> expect(r2dbc.sql("SELECT DEMO_HOLD('r2dbc') AS demo_value").map((row, metadata) -> row.get("DEMO_VALUE", Integer.class)).one().block(Duration.ofMinutes(2)), 7)),
        task(() -> jms.send("demo.orders", session -> {
          javax.jms.TextMessage message = session.createTextMessage("demo-order-" + cycle.id);
          message.setStringProperty("cycle", cycle.id);
          return message;
        })));
    }
    return cycle;
  }

  private CompletableFuture<Void> task(Runnable operation) {
    return CompletableFuture.runAsync(Context.current().wrap(() -> {
      try { operation.run(); }
      catch (RuntimeException error) { recordFailure(error); throw error; }
    }), workers);
  }

  private void finish(DemoCycle cycle) throws Exception {
    cycle.release();
    cycle.awaitFinished();
    completed.incrementAndGet();
    last = cycle;
    active = null;
  }

  private DemoCycle requireCycle(String id) {
    DemoCycle cycle = active;
    if (cycle == null || !cycle.id.equals(id)) throw new IllegalStateException("Unknown active demo cycle: " + id);
    return cycle;
  }

  private void runTraffic() {
    try {
      DemoCycle cycle;
      synchronized (this) {
        if (!trafficEnabled) return;
        checkHealthy();
        cycle = startCycle(true);
      }
      cycle.awaitEntered();
      Thread.sleep(3000);
      synchronized (this) { finish(cycle); }
    } catch (Exception error) {
      recordFailure(error);
      throw new CompletionException("Interactive demo traffic failed", error);
    }
  }

  private void checkHealthy() {
    if (!healthy()) throw new IllegalStateException("Demo workload is not healthy", failure.get());
  }

  private void recordFailure(Throwable error) {
    failure.compareAndSet(null, error);
    DemoCycle cycle = active;
    if (cycle != null) cycle.release();
  }

  private static void expect(Object actual, Object expected) {
    if (!expected.equals(actual)) throw new IllegalStateException("Expected " + expected + ", received " + actual);
  }

  @Override public void close() throws Exception {
    if (!closed.compareAndSet(false, true)) return;
    ready = false;
    trafficEnabled = false;
    DemoCycle cycle = active;
    if (cycle != null) cycle.release();
    traffic.shutdownNow();
    workers.shutdownNow();
    DemoResources.close(listener::stop, listener::destroy, factory::close, broker::stop,
        () -> reactor.netty.http.HttpResources.disposeLoopsAndConnectionsLater().block(TIMEOUT));
  }
}
