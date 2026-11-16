package com.flanksource.telltale.observer.sql;

import com.flanksource.telltale.observer.ObserverAdapter;
import com.flanksource.telltale.observer.ObserverRuntime;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.Statement;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;

public final class SqlObserver implements ObserverAdapter {
  private final Consumer<Map<String, Object>> emitter;
  private final BooleanSupplier enabled;
  private final SqlMetadata metadata;
  private final int activeLimit;
  private final Map<String, Execution> active = new ConcurrentHashMap<>();
  private final ThreadLocal<Integer> depth = ThreadLocal.withInitial(() -> 0);
  private final ThreadLocal<Integer> setupDepth = ThreadLocal.withInitial(() -> 0);
  private final ThreadLocal<Execution> current = new ThreadLocal<>();
  private final R2dbcSupport r2dbc = new R2dbcSupport();

  public SqlObserver() {
    this(new Options(row -> ObserverRuntime.emit("sql", row), () -> ObserverRuntime.armed("sql") || ObserverRuntime.armed("sql_top")));
  }

  public SqlObserver(Options options) {
    if (options.maxInflight <= 0 || options.maxMetadata <= 0) throw new IllegalArgumentException("SQL observer limits must be positive");
    this.emitter = options.emitter;
    this.enabled = options.enabled;
    this.activeLimit = options.maxInflight;
    this.metadata = new SqlMetadata(options.maxMetadata);
  }

  public static final class Options {
    final Consumer<Map<String, Object>> emitter;
    final BooleanSupplier enabled;
    public int maxInflight = 4096;
    public int maxMetadata = 16384;
    public Options(Consumer<Map<String, Object>> emitter, BooleanSupplier enabled) {
      this.emitter = emitter;
      this.enabled = enabled;
    }
  }

  public String id() { return "sql"; }
  public Set<String> kinds() { return new HashSet<>(Arrays.asList("sql", "sql_top")); }
  public void validate(String kind, Map<String, Object> params) { SqlProjection.validate(kind, params); }
  public Map<String, Object> project(String kind, Map<String, Object> params, Map<String, Object> row) {
    if (!kind.equals("sql_top")) return SqlProjection.project(kind, params, row);
    Object source = row.get("telltale.sql.source");
    if (!(source instanceof Execution)) throw new IllegalArgumentException("SQL top sample has no execution source");
    Map<String, Object> filter = new LinkedHashMap<>(row);
    filter.put("attributes", ((Execution) source).copy());
    if (SqlProjection.project(kind, params, filter) == null) return null;
    Map<String, Object> projected = new LinkedHashMap<>(row);
    projected.remove("telltale.sql.source");
    return projected;
  }

  public boolean matchesType(String name, Set<String> hierarchy) {
    return !Collections.disjoint(hierarchy, new HashSet<>(Arrays.asList("java.sql.Connection", "java.sql.Statement", "io.r2dbc.spi.Connection", "io.r2dbc.spi.Statement", "io.r2dbc.spi.Batch")));
  }

  public boolean matchesMethod(String name, String descriptor) {
    return new HashSet<>(Arrays.asList("execute", "executeQuery", "executeUpdate", "executeLargeUpdate", "executeBatch", "executeLargeBatch", "prepareStatement", "prepareCall", "createStatement", "createBatch", "addBatch", "clearBatch", "add", "bind", "bindNull", "close")).contains(name);
  }

  public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) {
    if (receiver == null) return null;
    if (!name.startsWith("execute")) {
      if (depth.get() > 0) return null;
      if ((name.startsWith("prepare") || name.startsWith("create")) && !(receiver instanceof Connection) && !implementsType(receiver.getClass(), "io.r2dbc.spi.Connection")) return null;
      int before = setupDepth.get();
      setupDepth.set(before + 1);
      if (before > 0) return new NestedSetup();
      return new Setup(receiver, name, args);
    }
    if (!enabled.getAsBoolean()) return null;
    if (receiver instanceof Statement) {
      int before = depth.get();
      if (before > 0) { depth.set(before + 1); return new Nested(); }
      Execution execution = begin(describe(receiver, "jdbc", args));
      if (execution == null) return null;
      execution.statement = receiver;
      execution.method = name;
      execution.batch = name.equals("executeBatch") || name.equals("executeLargeBatch");
      if (!execution.batch) { execution.description.remove("batchSize"); execution.description.remove("batchSql"); }
      depth.set(1);
      current.set(execution);
      return execution;
    }
    if (implementsType(receiver.getClass(), "io.r2dbc.spi.Statement") || implementsType(receiver.getClass(), "io.r2dbc.spi.Batch")) {
      int before = depth.get();
      if (before > 0) { depth.set(before + 1); return new Nested(); }
      Map<String, Object> description = describe(receiver, "r2dbc", args);
      if (implementsType(receiver.getClass(), "io.r2dbc.spi.Statement") && description.get("batchSize") instanceof Number)
        description.put("batchSize", ((Number) description.get("batchSize")).longValue() + 1L);
      depth.set(1);
      return new Reactive(receiver, name, args, description);
    }
    return null;
  }

  public Object onExit(Object token, Object result, Throwable error) {
    if (token instanceof NestedSetup) {
      leaveSetup();
    } else if (token instanceof Setup) {
      try { if (error == null) setup((Setup) token, result); }
      finally { leaveSetup(); }
    } else if (token instanceof Nested) {
      leave();
    } else if (token instanceof Execution) {
      leave();
      finish((Execution) token, result, error);
    } else if (token instanceof Reactive) {
      leave();
      Reactive reactive = (Reactive) token;
      if (error != null) finish(begin(reactive.description), null, error);
      else return r2dbc.observe(reactive.receiver, result, reactive.description,
          description -> description.put("telltale.execution", begin(description)),
          description -> {
            Execution execution = (Execution) description.get("telltale.execution");
            if (execution != null) synchronized (execution.description) { execution.description.put("phase", description.get("phase")); }
          },
          (description, failure) -> finish((Execution) description.get("telltale.execution"), null, failure),
          failure -> { ObserverRuntime.fail("sql", failure); ObserverRuntime.fail("sql_top", failure); });
    }
    return result;
  }

  private void leave() {
    int remaining = depth.get() - 1;
    if (remaining == 0) { depth.remove(); current.remove(); }
    else depth.set(remaining);
  }

  private void leaveSetup() {
    int remaining = setupDepth.get() - 1;
    if (remaining == 0) setupDepth.remove();
    else setupDepth.set(remaining);
  }

  private synchronized Execution begin(Map<String, Object> description) {
    if (!enabled.getAsBoolean()) return null;
    if (active.size() >= activeLimit) throw new IllegalStateException("SQL active execution limit exceeded: " + activeLimit);
    Execution execution = new Execution(description);
    active.put(execution.id, execution);
    return execution;
  }

  private void finish(Execution execution, Object result, Throwable error) {
    if (execution == null || active.remove(execution.id) == null) return;
    Map<String, Object> row = execution.row();
    if (error == null) row.put("rows", SqlRows.observed(execution.method, result));
    row.put("phase", error == null ? "completed" : "failed");
    if (error != null) row.put("error", error.getClass().getName());
    row.remove("telltale.execution");
    if (execution.batch) clearBatch(execution.statement);
    execution.statement = null;
    emitter.accept(row);
  }

  public List<Map<String, Object>> snapshot(String kind) {
    if (!kind.equals("sql_top")) return Collections.emptyList();
    List<Map<String, Object>> rows = new ArrayList<>();
    for (Execution execution : active.values()) {
      Map<String, Object> row = new LinkedHashMap<>(), attributes = profileDescription(execution);
      attributes.put("startedAt", execution.startedAt);
      attributes.remove("telltale.execution");
      row.put("operationId", execution.id);
      row.put("elapsedNs", System.nanoTime() - execution.startedNanos);
      row.put("sampledAtNanos", ObserverRuntime.epochNanos());
      row.put("attributes", attributes);
      row.put("telltale.sql.source", execution);
      if (attributes.containsKey("traceId")) row.put("traceId", attributes.get("traceId"));
      if (attributes.containsKey("spanId")) row.put("spanId", attributes.get("spanId"));
      rows.add(row);
    }
    return rows;
  }

  public synchronized void onDisarm(String kind) {
    if (!enabled.getAsBoolean()) active.clear();
  }

  public List<Map<String, Object>> terminalRows(String kind, Map<String, Object> params) {
    if (!kind.equals("sql")) return Collections.emptyList();
    List<Map<String, Object>> rows = new ArrayList<>();
    for (Execution execution : active.values()) {
      Map<String, Object> row = execution.row();
      row.put("incomplete", true);
      row.put("completion", "capture-ended");
      rows.add(row);
    }
    return rows;
  }

  private static Map<String, Object> profileDescription(Execution execution) {
    Map<String, Object> description = execution.copy();
    description.remove("batchSql");
    Object query = description.get("sql");
    if (query instanceof String) {
      String sql = (String) query;
      try {
        byte[] hash = MessageDigest.getInstance("SHA-256").digest(sql.getBytes(StandardCharsets.UTF_8));
        StringBuilder fingerprint = new StringBuilder();
        for (byte value : hash) fingerprint.append(String.format(Locale.ROOT, "%02x", value & 255));
        description.put("queryFingerprint", fingerprint.toString());
      } catch (java.security.NoSuchAlgorithmException error) { throw new IllegalStateException("SHA-256 is unavailable", error); }
      if (sql.length() > 1024) {
        int end = Character.isHighSurrogate(sql.charAt(1023)) ? 1023 : 1024;
        description.put("sql", sql.substring(0, end));
        description.put("sqlTruncated", true);
      }
    }
    return description;
  }

  @SuppressWarnings("unchecked")
  public void onOperation(Map<String, Object> metadata) {
    Execution execution = current.get();
    if (execution == null || !"sql".equals(metadata.get("signal"))) return;
    Map<String, Object> attributes = (Map<String, Object>) metadata.get("attributes");
    synchronized (execution.description) {
      if (attributes != null) {
        Object sql = attributes.containsKey("db.query.text") ? attributes.get("db.query.text") : attributes.get("db.statement");
        if (sql instanceof String && !((String) sql).trim().isEmpty()) {
          execution.description.put("sql", SqlText.sanitize((String) sql));
          execution.description.remove("sqlUnavailableReason");
        }
        Object database = attributes.containsKey("db.namespace") ? attributes.get("db.namespace") : attributes.get("db.name");
        if (database instanceof String) execution.description.put("database", database);
        Object engine = attributes.containsKey("db.system.name") ? attributes.get("db.system.name") : attributes.get("db.system");
        if (engine instanceof String) execution.description.put("engine", engine);
      }
      if (metadata.get("traceId") instanceof String) execution.description.put("traceId", metadata.get("traceId"));
      if (metadata.get("spanId") instanceof String) execution.description.put("spanId", metadata.get("spanId"));
    }
  }

  private Map<String, Object> describe(Object receiver, String api, Object[] args) {
    Map<String, Object> description = metadata.get(receiver);
    if (description == null) description = new LinkedHashMap<>();
    description.put("api", api);
    if (!description.containsKey("driver")) description.put("driver", receiver.getClass().getName());
    description.put("phase", "executing");
    if (args.length > 0 && args[0] instanceof String) {
      description.put("sql", SqlText.sanitize((String) args[0]));
      description.remove("sqlUnavailableReason");
    }
    if (!description.containsKey("sql")) description.put("sqlUnavailableReason", api.equals("jdbc") && receiver instanceof PreparedStatement ? "prepared-before-instrumentation" : "statement-before-instrumentation");
    return description;
  }

  private void setup(Setup setup, Object result) {
    if ((setup.name.startsWith("prepare") || setup.name.equals("createStatement") || setup.name.equals("createBatch")) && result != null) {
      Map<String, Object> description = metadata.get(setup.receiver);
      if (description == null) {
        description = new LinkedHashMap<>();
        description.put("connection", "connection/" + UUID.randomUUID());
        metadata.put(setup.receiver, description);
      }
      description = new LinkedHashMap<>(description);
      description.put("driver", result.getClass().getName());
      if (setup.args.length > 0 && setup.args[0] instanceof String) description.put("sql", SqlText.sanitize((String) setup.args[0]));
      metadata.put(result, description);
    } else if (setup.name.equals("close") && (setup.receiver instanceof Connection || setup.receiver instanceof Statement)) metadata.remove(setup.receiver);
    else if (setup.name.equals("addBatch") || setup.name.equals("add")) addBatch(setup);
    else if (setup.name.equals("clearBatch")) clearBatch(setup.receiver);
  }

  @SuppressWarnings("unchecked")
  private void addBatch(Setup setup) {
    Map<String, Object> description = metadata.get(setup.receiver);
    if (description == null) description = new LinkedHashMap<>();
    Number count = (Number) description.get("batchSize");
    description.put("batchSize", count == null ? 1L : count.longValue() + 1L);
    if (setup.args.length > 0 && setup.args[0] instanceof String) {
      List<String> queries = new ArrayList<>((List<String>) description.getOrDefault("batchSql", Collections.emptyList()));
      String query = SqlText.sanitize((String) setup.args[0]);
      if (queries.size() >= 1024 || query.length() + String.valueOf(description.getOrDefault("sql", "")).length() > 65536)
        throw new IllegalStateException("SQL batch metadata limit exceeded");
      queries.add(query);
      description.put("batchSql", queries);
      description.put("sql", String.join("; ", queries));
    }
    metadata.put(setup.receiver, description);
  }

  private void clearBatch(Object statement) {
    Map<String, Object> description = metadata.get(statement);
    if (description == null) return;
    if (description.containsKey("batchSql")) description.remove("sql");
    description.remove("batchSize");
    description.remove("batchSql");
    metadata.put(statement, description);
  }

  private static boolean implementsType(Class<?> type, String expected) {
    if (type == null) return false;
    if (type.getName().equals(expected)) return true;
    for (Class<?> implemented : type.getInterfaces()) if (implementsType(implemented, expected)) return true;
    return implementsType(type.getSuperclass(), expected);
  }

  private static final class Setup {
    final Object receiver;
    final String name;
    final Object[] args;
    Setup(Object receiver, String name, Object[] args) { this.receiver = receiver; this.name = name; this.args = args; }
  }
  private static final class Nested {}
  private static final class NestedSetup {}
  private static final class Reactive {
    final Object receiver;
    final Map<String, Object> description;
    Reactive(Object receiver, String name, Object[] args, Map<String, Object> description) { this.receiver = receiver; this.description = description; }
  }
  private static final class Execution {
    final String id = UUID.randomUUID().toString();
    final String startedAt = Instant.now().toString();
    final long startedNanos = System.nanoTime();
    final Map<String, Object> description;
    Object statement;
    String method;
    boolean batch;
    Execution(Map<String, Object> description) { this.description = new LinkedHashMap<>(description); }
    Map<String, Object> copy() { synchronized (description) { return new LinkedHashMap<>(description); } }
    Map<String, Object> row() {
      Map<String, Object> row = copy();
      row.put("operationId", id);
      row.put("startedAt", startedAt);
      row.put("durationNs", System.nanoTime() - startedNanos);
      row.put("rows", -1L);
      row.remove("telltale.execution");
      return row;
    }
  }
}
