package com.flanksource.telltale.observer.sql;

import io.r2dbc.proxy.callback.*;
import io.r2dbc.proxy.core.*;
import io.r2dbc.proxy.listener.ProxyMethodExecutionListener;
import io.r2dbc.spi.*;
import java.util.*;
import java.util.function.BiConsumer;
import java.util.function.Consumer;
import org.reactivestreams.Publisher;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

/** Loaded beside the application's R2DBC SPI to preserve classloader identity. */
public final class R2dbcBridge {
  private R2dbcBridge() {}

  public static Object observe(Object statement, Object original, Map<String, Object> metadata,
      Consumer<Map<String, Object>> started, Consumer<Map<String, Object>> changed,
      BiConsumer<Map<String, Object>, Throwable> ended, Consumer<Throwable> failed) {
    if (!(original instanceof Publisher)) throw new IllegalArgumentException("R2DBC execute did not return a Publisher");
    Flux<?> observed = Flux.defer(() -> wrap(statement, (Publisher<?>) original, metadata, started, changed, ended, failed));
    return original instanceof Mono ? observed.singleOrEmpty() : observed;
  }

  @SuppressWarnings("unchecked")
  private static Publisher<Object> wrap(Object statement, Publisher<?> original, Map<String, Object> metadata,
      Consumer<Map<String, Object>> started, Consumer<Map<String, Object>> changed,
      BiConsumer<Map<String, Object>, Throwable> ended, Consumer<Throwable> failed) {
    try {
      ProxyConfig config = new ProxyConfig();
      config.addListener(new QueryListener(metadata, started, changed, ended, failed));
      ConnectionContext connection = new ConnectionContext((String) metadata.get("connection"));
      Object result;
      if (statement instanceof Statement) {
        StatementCallbackHandler handler = new StatementCallbackHandler((Statement) statement,
            new StatementContext(connection, (String) metadata.get("sql")), connection, config);
        handler.setMethodInvocationStrategy((method, target, args) -> original);
        result = handler.invoke(statement, Statement.class.getMethod("execute"), new Object[0]);
      } else if (statement instanceof Batch) {
        BatchCallbackHandler handler = new BatchCallbackHandler((Batch) statement, connection, config);
        handler.setMethodInvocationStrategy((method, target, args) -> original);
        result = handler.invoke(statement, Batch.class.getMethod("execute"), new Object[0]);
      } else throw new IllegalArgumentException("unsupported R2DBC execution receiver");
      return (Publisher<Object>) result;
    } catch (Throwable error) {
      report(failed, error);
      return (Publisher<Object>) original;
    }
  }

  private static void report(Consumer<Throwable> failed, Throwable error) {
    try { failed.accept(error); }
    catch (Throwable reportingFailure) { if (error != reportingFailure) error.addSuppressed(reportingFailure); }
  }

  private static final class QueryListener implements ProxyMethodExecutionListener {
    final Map<String, Object> metadata;
    final Consumer<Map<String, Object>> started, changed;
    final BiConsumer<Map<String, Object>, Throwable> ended;
    final Consumer<Throwable> failed;
    Map<String, Object> execution;
    final Set<MethodExecutionInfo> consumers = Collections.newSetFromMap(new IdentityHashMap<>());
    boolean produced;
    QueryListener(Map<String, Object> metadata, Consumer<Map<String, Object>> started,
        Consumer<Map<String, Object>> changed, BiConsumer<Map<String, Object>, Throwable> ended, Consumer<Throwable> failed) {
      this.metadata = metadata; this.started = started; this.changed = changed; this.ended = ended; this.failed = failed;
    }
    public synchronized void beforeQuery(QueryExecutionInfo info) {
      try {
        execution = new LinkedHashMap<>(metadata);
        execution.put("phase", "executing");
        started.accept(execution);
      } catch (Throwable error) { report(failed, error); }
    }
    public void afterQuery(QueryExecutionInfo info) {
      try { ended.accept(execution, info.getThrowable()); }
      catch (Throwable error) { report(failed, error); }
    }
    public void afterExecuteOnStatement(MethodExecutionInfo info) { produced(); }
    public void afterExecuteOnBatch(MethodExecutionInfo info) { produced(); }
    public void beforeMapOnResult(MethodExecutionInfo info) { consuming(info, true); }
    public void afterMapOnResult(MethodExecutionInfo info) { consuming(info, false); }
    public void beforeGetRowsUpdatedOnResult(MethodExecutionInfo info) { consuming(info, true); }
    public void afterGetRowsUpdatedOnResult(MethodExecutionInfo info) { consuming(info, false); }
    public void beforeFlatMapOnResult(MethodExecutionInfo info) { consuming(info, true); }
    public void afterFlatMapOnResult(MethodExecutionInfo info) { consuming(info, false); }
    private synchronized void produced() {
      produced = true;
      phase();
    }
    private synchronized void consuming(MethodExecutionInfo info, boolean entering) {
      try {
        if (entering) {
          if (!consumers.contains(info) && consumers.size() >= 4096) throw new IllegalStateException("R2DBC active result-consumption limit exceeded");
          consumers.add(info);
        } else consumers.remove(info);
        phase();
      } catch (Throwable error) { report(failed, error); }
    }
    private void phase() {
      try {
        if (execution == null) return;
        execution.put("phase", !consumers.isEmpty() ? "consuming" : produced ? "awaiting-consumption" : "executing");
        changed.accept(execution);
      } catch (Throwable error) { report(failed, error); }
    }
  }

  private static final class StatementContext implements StatementInfo {
    final ConnectionInfo connection;
    final String query;
    final ValueStore values = ValueStore.create();
    StatementContext(ConnectionInfo connection, String query) { this.connection = connection; this.query = query; }
    public ConnectionInfo getConnectionInfo() { return connection; }
    public String getOriginalQuery() { return query == null ? "" : query; }
    public String getUpdatedQuery() { return getOriginalQuery(); }
    public ValueStore getValueStore() { return values; }
  }

  private static final class ConnectionContext implements ConnectionInfo {
    final String id;
    final ValueStore values = ValueStore.create();
    int transactions, commits, rollbacks;
    boolean closed;
    ConnectionContext(String id) { this.id = id; }
    public Connection getOriginalConnection() { return null; }
    public String getConnectionId() { return id; }
    public void incrementTransactionCount() { transactions++; }
    public void incrementCommitCount() { commits++; }
    public void incrementRollbackCount() { rollbacks++; }
    public int getTransactionCount() { return transactions; }
    public int getCommitCount() { return commits; }
    public int getRollbackCount() { return rollbacks; }
    public boolean isClosed() { return closed; }
    public void setClosed(boolean value) { closed = value; }
    public ValueStore getValueStore() { return values; }
  }
}
