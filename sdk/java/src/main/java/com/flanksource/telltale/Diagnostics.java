package com.flanksource.telltale;

import com.flanksource.telltale.diag.hotpatch.*;
import com.flanksource.telltale.diag.jmx.*;
import com.flanksource.telltale.diag.jvm.*;
import java.util.*;

/** Shared operation dispatcher for the agent and servlet adapters. */
public final class Diagnostics {
  public interface Operation {
    Object call(Map<String, Object> params) throws Exception;
  }

  private final Map<String, Operation> operations = new LinkedHashMap<>();
  private final Map<String, Scope> scopes = new HashMap<>();
  private final boolean allowMutation;
  private final JmxService jmx = new JmxService();
  private final Extensions extensions;
  private final ClassOperations classes = new ClassOperations();

  void close() throws Exception { extensions.close(); }
  private final NotificationHub notifications =
      new NotificationHub(java.lang.management.ManagementFactory.getPlatformMBeanServer());

  public Diagnostics(Map<String, Object> config) throws Exception {
    allowMutation = Boolean.TRUE.equals(config.get("allowMutation"));
    extensions = new Extensions(text(config, "extensionsDir", null));
    register("jvm.extensions.list", Scope.READ, p -> extensions.list());
    register("jvm.extensions.install", Scope.INSTRUMENT, p -> extensions.install(p, false));
    register("jvm.extensions.replace", Scope.INSTRUMENT, p -> extensions.install(p, true));
    register("jvm.extensions.unload", Scope.INSTRUMENT, p -> extensions.unload(required(p, "id")));
    register("jvm.catalog", Scope.READ, p -> catalog());
    register(
        "jvm.status",
        Scope.READ,
        p -> {
          Map<String, Object> status = new LinkedHashMap<>();
          status.put("allowMutation", allowMutation);
          status.put("instrumentation", InstrumentationService.installed());
          status.put("observers", com.flanksource.telltale.observer.ObserverRuntime.capabilities());
          if (InstrumentationService.installed())
            status.put(
                "patches",
                new PatchService(InstrumentationService.instrumentation()).patches(index()));
          return status;
        });
    register("jvm.jmx.search", Scope.READ, p -> jmx.search(text(p, "pattern", "")));
    register("jvm.jmx.info", Scope.READ, p -> jmx.info(required(p, "objectName")));
    register(
        "jvm.jmx.read",
        Scope.READ,
        p ->
            p.containsKey("attribute")
                ? jmx.read(required(p, "objectName"), required(p, "attribute"))
                : jmx.readAll(required(p, "objectName")));
    register(
        "jvm.jmx.write",
        Scope.WRITE,
        p -> {
          jmx.write(required(p, "objectName"), required(p, "attribute"), p.get("value"));
          return Collections.singletonMap("applied", true);
        });
    register(
        "jvm.jmx.invoke",
        Scope.WRITE,
        p ->
            jmx.invoke(
                required(p, "objectName"),
                required(p, "operation"),
                list(p, "args").toArray(),
                list(p, "signature").toArray(new String[0])));
    register("jvm.metrics", Scope.READ, p -> new JvmMetricsService().snapshot());
    register("jvm.settings", Scope.READ, p -> SettingsService.snapshot());
    register("jvm.resources", Scope.READ, p -> ResourceSettings.snapshot());
    register("jvm.flags", Scope.READ, p -> SettingsService.command("vmFlags", "-all"));
    register(
        "jvm.threads",
        Scope.READ,
        p ->
            new ThreadDumpService()
                .snapshot(((Number) p.getOrDefault("sampleMs", 100)).intValue()));
    register("jvm.threads.dump", Scope.READ, p -> new ThreadDumpService().dump());
    register("jvm.heap.histogram", Scope.READ, p -> new HeapHistogramService().histogram(p));
    register(
        "jvm.threads.stack",
        Scope.READ,
        p -> {
          Object id = p.get("id");
          if (!(id instanceof Number)) throw new IllegalArgumentException("id must be an integer");
          try {
            return new ThreadDumpService()
                .stack(new java.math.BigDecimal(id.toString()).longValueExact());
          } catch (ArithmeticException | NumberFormatException error) {
            throw new IllegalArgumentException("id must be a 64-bit integer", error);
          }
        });
    register("jvm.logging.list", Scope.READ, p -> new LoggingService().loggers());
    register(
        "jvm.logging.set",
        Scope.WRITE,
        p -> {
          if (!(p.get("name") instanceof String))
            throw new IllegalArgumentException("name must be a string (empty names the root)");
          if (!p.containsKey("level")) throw new IllegalArgumentException("level is required");
          new LoggingService().setLevel((String) p.get("name"), text(p, "level", null));
          return Collections.singletonMap("applied", true);
        });
    register(
        "jvm.classes",
        Scope.READ,
        p ->
            index()
                .page(
                    text(p, "loader", null),
                    text(p, "query", ""),
                    ((Number) p.getOrDefault("limit", 200)).intValue(),
                    ((Number) p.getOrDefault("offset", 0)).intValue(),
                    Boolean.TRUE.equals(p.get("includePlatform"))));
    register("jvm.loaders", Scope.READ, p -> index().loaders());
    register(
        "jvm.groovy.eval",
        Scope.EVAL,
        p -> {
          ClassLoader application = loader(p);
          try (Extensions.BindingsLease bindings = extensions.bindings(application)) {
            return Scripts.evaluate(p, application, jmx, bindings.values);
          }
        });
    register("jvm.ognl.eval", Scope.EVAL, p -> OgnlEvaluation.evaluate(p, loader(p)));
    register("jvm.classes.source", Scope.READ, p -> classes.source(new JsonBody(p)));
    register(
        "jvm.classes.bytecode",
        Scope.READ,
        p ->
            Collections.singletonMap(
                "bytecode", Base64.getEncoder().encodeToString(classes.bytecode(new JsonBody(p)))));
    register("jvm.classes.compile", Scope.READ, p -> classes.compile(new JsonBody(p)));
    register("jvm.classes.patch", Scope.INSTRUMENT, p -> classes.patch(new JsonBody(p)));
    register("jvm.classes.instrument", Scope.INSTRUMENT, p -> classes.instrument(new JsonBody(p)));
    register("jvm.classes.restore", Scope.INSTRUMENT, p -> classes.restore(new JsonBody(p)));
    register("jvm.classes.lease", Scope.INSTRUMENT, p -> classes.lease(new JsonBody(p)));
    register(
        "jvm.classes.patches",
        Scope.READ,
        p -> new PatchService(InstrumentationService.instrumentation()).patches(index()));
    register("jvm.probes.read", Scope.READ, p -> classes.probes(new JsonBody(p)));
    register("jvm.probes.list", Scope.READ, p -> classes.probeKeys());
    register("jvm.probes.freeze", Scope.TRACE, p -> classes.freezeProbe(new JsonBody(p)));
    register("jvm.notifications.list", Scope.READ, p -> notifications.list());
  }

  public synchronized void register(String name, Scope scope, Operation operation) {
    if (operations.containsKey(name))
      throw new IllegalArgumentException("duplicate operation: " + name);
    operations.put(name, operation);
    scopes.put(name, Objects.requireNonNull(scope, "scope"));
  }

  public synchronized List<Map<String, Object>> catalog() {
    List<Map<String, Object>> result = new ArrayList<>();
    for (String name : operations.keySet()) {
      Map<String, Object> spec =
          OperationSpec.named(name)
              .description(DiagnosticSchemas.description(name))
              .command(DiagnosticSchemas.command(name))
              .parameters(DiagnosticSchemas.parameters(name))
              .result(DiagnosticSchemas.result(name))
              .mutates(scopes.get(name).mutates())
              .descriptor();
      spec.put("scope", scopes.get(name).wire());
      result.add(spec);
    }
    result.addAll(extensions.catalog());
    return result;
  }

  /** The scope a caller needs for an operation; unknown operations fail closed. */
  public Scope scope(String name) {
    if (name.startsWith("extension.")) return extensions.scope(name);
    synchronized (this) {
      Scope scope = scopes.get(name);
      if (scope == null) throw new IllegalArgumentException("unknown operation: " + name);
      return scope;
    }
  }

  public Object call(String name, Map<String, Object> params, boolean callerMutation)
      throws Exception {
    if (name.startsWith("extension.")) {
      return extensions.call(name, params, allowMutation && callerMutation);
    }
    Operation operation;
    synchronized (this) {
      operation = operations.get(name);
      if (operation == null) throw new IllegalArgumentException("unknown operation: " + name);
      if (scopes.get(name).mutates() && (!allowMutation || !callerMutation))
        throw new IllegalStateException("mutation disabled for " + name);
    }
    return operation.call(params);
  }

  private ClassIndex index() {
    return ClassIndex.from(InstrumentationService.instrumentation());
  }

  Capture capture(String kind, Map<String, Object> params, String key, boolean callerMutation,
      Map<String, Object> resource)
      throws Exception {
    if (RuntimeService.OBSERVATIONS.contains(kind))
      return new ObserverCapture(kind, params, key, resource);
    if (kind.equals("jvm_notifications")) return new NotificationCapture(notifications, params);
    if (!allowMutation || !callerMutation)
      throw new IllegalStateException("mutation disabled for " + kind);
    if (kind.equals("jvm_ognl")) return new ExpressionCapture(params, loader(params));
    if (!kind.equals("jvm_trace") && !kind.equals("jvm_redefine"))
      throw new IllegalArgumentException("unknown capture kind: " + kind);
    return new MethodCapture(classes, kind, params, key);
  }

  private ClassLoader loader(Map<String, Object> p) {
    if (!p.containsKey("loader")) return ClassLoader.getSystemClassLoader();
    String hash = required(p, "loader");
    for (Class<?> type : InstrumentationService.instrumentation().getAllLoadedClasses()) {
      if (ClassIndex.hash(type.getClassLoader()).equals(hash)) return type.getClassLoader();
    }
    throw new IllegalArgumentException("unknown loader: " + hash);
  }

  static String required(Map<String, Object> p, String key) {
    String value = text(p, key, "");
    if (value.trim().isEmpty()) throw new IllegalArgumentException(key + " is required");
    return value;
  }

  static String text(Map<String, Object> p, String key, String defaultValue) {
    Object value = p.get(key);
    if (value == null) return defaultValue;
    if (!(value instanceof String)) throw new IllegalArgumentException(key + " must be a string");
    return (String) value;
  }

  private static List<?> list(Map<String, Object> p, String key) {
    Object value = p.get(key);
    if (value == null) return Collections.emptyList();
    if (!(value instanceof List)) throw new IllegalArgumentException(key + " must be an array");
    return (List<?>) value;
  }
}
