package com.flanksource.telltale.observer;

import com.google.gson.Gson;
import java.io.*;
import java.lang.instrument.Instrumentation;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.BiFunction;
import java.util.jar.JarFile;

/** JVM-wide passive observer owner, shared by all captures and protocol adapters. */
public final class ObserverRuntime {
  private static final Map<String, ObserverAdapter> ADAPTERS = new LinkedHashMap<>();
  private static final CopyOnWriteArrayList<ObserverSubscription> CAPTURES = new CopyOnWriteArrayList<>();
  private static final Gson JSON = new Gson();
  private static final Object MUTATIONS = new Object();
  public static final String SOURCE = source();
  private static final OtelOperations OPERATIONS = new OtelOperations();
  private static Instrumentation instrumentation;
  private static ObserverTransformer transformer;
  private static List<ObserverAdapter> installedAdapters = Collections.emptyList();
  private static volatile List<Map<String, Object>> retiredLoaders = Collections.emptyList();
  private static JarFile bootstrap;
  private static final java.util.concurrent.ScheduledExecutorService EXPIRY =
      java.util.concurrent.Executors.newSingleThreadScheduledExecutor(job -> {
        Thread thread = new Thread(job, "telltale-observer-leases");
        thread.setDaemon(true); thread.setContextClassLoader(null); return thread;
      });
  private ObserverRuntime() {}

  private static String source() {
    Properties properties = System.getProperties();
    synchronized (properties) {
      String key = "telltale.observer.source.instance";
      String value = properties.getProperty(key);
      if (value == null) { value = UUID.randomUUID().toString(); properties.setProperty(key, value); }
      return value;
    }
  }

  public static synchronized void install(Instrumentation installed) throws Exception {
    if (foreignLoader()) {
      owner().getMethod("install", Instrumentation.class).invoke(null, installed);
      return;
    }
    if (instrumentation != null) {
      return;
    }
    if (!installed.isRetransformClassesSupported())
      throw new IllegalStateException("observer requires JVM class retransformation support");
    String configured = System.getProperty("telltale.observer.bootstrap");
    Path path;
    if (configured != null) path = Paths.get(configured);
    else {
      try (InputStream resource = ObserverRuntime.class.getResourceAsStream("/telltale-observer-bootstrap.jar")) {
        if (resource == null) throw new IllegalStateException("observer bootstrap JAR is missing");
        Path directory = Paths.get(System.getProperty("telltale.runtime.dir", ".tmp"));
        Files.createDirectories(directory);
        path = Files.createTempFile(directory, "telltale-observer-", ".jar");
        Files.copy(resource, path, StandardCopyOption.REPLACE_EXISTING);
        path.toFile().deleteOnExit();
      }
    }
    bootstrap = new JarFile(path.toFile());
    installed.appendToBootstrapClassLoaderSearch(bootstrap);
    Class<?> bridge = Class.forName("com.flanksource.telltale.observer.bootstrap.ObserverBridge", true, null);
    bridge.getField("handler").set(null, (BiFunction<String, Object[], Object>) ObserverRuntime::dispatch);
    com.flanksource.telltale.diag.hotpatch.SourceTransformations.install(installed);
    OtelTargets.name("instrumenter");
    transformer = new ObserverTransformer();
    register(new OtelSpans());
    installed.addTransformer(transformer, true);
    instrumentation = installed;
    EXPIRY.scheduleWithFixedDelay(ObserverRuntime::expire, 50, 50, java.util.concurrent.TimeUnit.MILLISECONDS);
  }

  public static synchronized void register(ObserverAdapter adapter) {
    if (ADAPTERS.containsKey(adapter.id())) {
      if (ADAPTERS.get(adapter.id()).getClass() == adapter.getClass()) return;
      throw new IllegalArgumentException("duplicate observer adapter " + adapter.id());
    }
    ADAPTERS.put(adapter.id(), adapter);
  }

  public static ObserverSubscription subscribe(String kind, String id, Map<String, Object> params)
      throws Exception {
    synchronized (MUTATIONS) {
    if (instrumentation == null) throw new IllegalStateException("observer requires an attached Telltale agent");
    loadAdapter(kind);
    for (ObserverAdapter adapter : adapters()) if (adapter.kinds().contains(kind)) adapter.validate(kind, params);
    for (ObserverSubscription capture : CAPTURES)
      if (capture.id.equals(id)) throw new IllegalArgumentException("duplicate observer capture " + id);
    ObserverSubscription subscription = new ObserverSubscription(kind, id, params);
    CAPTURES.add(subscription);
    try {
      refresh();
      if (kind.equals("spans") && transformer.matched("otel-spans") == 0)
        throw new IllegalStateException("no compatible loaded OpenTelemetry SpanExporter; JVM span mirroring requires an existing SDK exporter");
    }
    catch (Throwable error) {
      CAPTURES.remove(subscription);
      try { refresh(); } catch (Throwable cleanup) { if (cleanup != error) error.addSuppressed(cleanup); }
      throwFailure(error);
    }
    subscription.startLease();
    return subscription;
    }
  }

  private static void loadAdapter(String kind) throws Exception {
    for (ObserverAdapter adapter : adapters()) if (adapter.kinds().contains(kind)) return;
    String name;
    switch (kind) {
      case "http": case "http_top": name = "http.HttpObserver"; break;
      case "jms": name = "jms.JmsObserver"; break;
      case "sql": case "sql_top": name = "sql.SqlObserver"; break;
      default: throw new IllegalArgumentException("unknown observer kind " + kind);
    }
    register((ObserverAdapter) Class.forName("com.flanksource.telltale.observer." + name).getConstructor().newInstance());
  }

  static void release(ObserverSubscription capture) throws Exception {
    synchronized (MUTATIONS) {
      capture.stopping = true;
      Throwable failure = null;
      for (ObserverAdapter adapter : adapters()) {
        if (!adapter.kinds().contains(capture.kind)) continue;
        try { for (Map<String, Object> row : adapter.terminalRows(capture.kind, capture.params)) capture.emit(row, true); }
        catch (Throwable error) { if (failure == null) failure = error; else if (failure != error) failure.addSuppressed(error); }
        try { if (!armed(capture.kind)) adapter.onDisarm(capture.kind); }
        catch (Throwable error) { if (failure == null) failure = error; else if (failure != error) failure.addSuppressed(error); }
      }
      CAPTURES.remove(capture);
      try { refresh(); }
      catch (Throwable error) { if (failure == null) failure = error; else if (failure != error) failure.addSuppressed(error); }
      if (CAPTURES.isEmpty()) OPERATIONS.clear();
      if (failure != null) throwFailure(failure);
    }
  }

  private static void throwFailure(Throwable failure) throws Exception {
    if (failure instanceof Error) throw (Error) failure;
    if (failure instanceof Exception) throw (Exception) failure;
    throw new IllegalStateException("observer lifecycle failed", failure);
  }

  private static void refresh() throws Exception {
    Set<String> active = new LinkedHashSet<>();
    for (ObserverSubscription capture : CAPTURES) active.add(capture.kind);
    List<ObserverAdapter> adapters = new ArrayList<>();
    for (ObserverAdapter adapter : adapters())
      if (!Collections.disjoint(adapter.kinds(), active)) adapters.add(adapter);
    List<ObserverAdapter> changed = new ArrayList<>();
    for (ObserverAdapter adapter : installedAdapters) if (!adapters.contains(adapter)) changed.add(adapter);
    for (ObserverAdapter adapter : adapters) if (!installedAdapters.contains(adapter)) changed.add(adapter);
    Class<?>[] loaded = instrumentation.getAllLoadedClasses();
    Map<ClassLoader, Map<String, Object>> lifecycle = loaderStates(loaded);
    if (changed.isEmpty()) { transformer.check(); return; }
    boolean operationsChanged = installedAdapters.isEmpty() != adapters.isEmpty();
    transformer.configure(adapters, !adapters.isEmpty());
    installedAdapters = adapters;
    List<Class<?>> targets = new ArrayList<>();
    for (Class<?> type : loaded) {
      if (type.isArray() || type.isPrimitive()) continue;
      if (lifecycle.get(type.getClassLoader()) != null) continue;
      if (instrumentation.isModifiableClass(type) && transformer.candidate(type, changed, operationsChanged)) targets.add(type);
    }
    if (!targets.isEmpty()) instrumentation.retransformClasses(targets.toArray(new Class<?>[0]));
    transformer.check();
  }

  private static Map<ClassLoader, Map<String, Object>> loaderStates(Class<?>[] loaded) {
    Map<ClassLoader, Map<String, Object>> lifecycle = new IdentityHashMap<>();
    for (Class<?> type : loaded) {
      if (type.isArray() || type.isPrimitive()) continue;
      ClassLoader loader = type.getClassLoader();
      if (!lifecycle.containsKey(loader)) lifecycle.put(loader, LoaderLifecycle.retired(loader));
      Map<String, Object> retired = lifecycle.get(loader);
      if (retired != null) retired.put("classes", ((Number) retired.getOrDefault("classes", 0)).intValue() + 1);
    }
    List<Map<String, Object>> retired = new ArrayList<>();
    for (Map<String, Object> state : lifecycle.values()) if (state != null) retired.add(state);
    retiredLoaders = Collections.unmodifiableList(retired);
    return lifecycle;
  }

  public static boolean armed(String kind) {
    for (ObserverSubscription capture : CAPTURES) if (!capture.stopping && capture.kind.equals(kind)) return true;
    return false;
  }

  public static Map<String, Object> options(String kind) {
    Map<String, Object> result = new LinkedHashMap<>();
    for (ObserverSubscription capture : CAPTURES) {
      if (capture.stopping || !capture.kind.equals(kind)) continue;
      for (Map.Entry<String, Object> option : capture.params.entrySet()) {
        Object before = result.get(option.getKey()), value = option.getValue();
        if (value instanceof Boolean) value = Boolean.TRUE.equals(before) || Boolean.TRUE.equals(value);
        else if (before instanceof Number && value instanceof Number)
          value = Math.max(((Number) before).longValue(), ((Number) value).longValue());
        result.put(option.getKey(), value);
      }
    }
    return result;
  }

  public static List<Map<String, Object>> captureOptions(String kind) {
    List<Map<String, Object>> result = new ArrayList<>();
    for (ObserverSubscription capture : CAPTURES) if (!capture.stopping && capture.kind.equals(kind)) result.add(capture.params);
    return Collections.unmodifiableList(result);
  }

  public static int bodyLimit(String kind) {
    Map<String, Object> params = options(kind);
    if (!Boolean.TRUE.equals(params.get("body"))) return 0;
    Object value = params.containsKey("bodyMaxBytes") ? params.get("bodyMaxBytes") : params.get("maxBodyBytes");
    return value instanceof Number && ((Number) value).intValue() > 0
        ? Math.min(1 << 20, ((Number) value).intValue()) : 65536;
  }

  public static void emit(String kind, Map<String, Object> row) {
    for (ObserverSubscription capture : CAPTURES) if (!capture.stopping && capture.kind.equals(kind)) capture.emit(row, false);
  }

  public static void fail(String kind, Throwable error) {
    for (ObserverSubscription capture : CAPTURES)
      if (kind == null || capture.kind.equals(kind)) capture.fail(error);
  }

  static List<Map<String, Object>> snapshot(String kind) {
    List<Map<String, Object>> rows = new ArrayList<>();
    for (ObserverAdapter adapter : adapters()) if (adapter.kinds().contains(kind)) rows.addAll(adapter.snapshot(kind));
    return rows;
  }

  static Map<String, Object> project(String kind, Map<String, Object> params, Map<String, Object> row) {
    for (ObserverAdapter adapter : adapters()) {
      if (adapter.kinds().contains(kind)) row = adapter.project(kind, params, row);
      if (row == null) return null;
    }
    return row;
  }

  public static String json(Object value) { return JSON.toJson(value); }
  public static long epochNanos() {
    java.time.Instant now = java.time.Instant.now();
    return Math.addExact(Math.multiplyExact(now.getEpochSecond(), 1000000000L), now.getNano());
  }
  public static boolean installed() {
    if (!foreignLoader()) return instrumentation != null;
    try { return Boolean.TRUE.equals(owner().getMethod("installed").invoke(null)); }
    catch (ClassNotFoundException absent) { return false; }
    catch (ReflectiveOperationException error) { throw new IllegalStateException("read JVM observer owner", error); }
  }
  public static Map<String, Object> capabilities() {
    if (foreignLoader()) {
      try { return (Map<String, Object>) owner().getMethod("capabilities").invoke(null); }
      catch (ClassNotFoundException absent) { return Collections.singletonMap("installed", false); }
      catch (ReflectiveOperationException error) { throw new IllegalStateException("read JVM observer capabilities", error); }
    }
    Map<String, Object> result = new LinkedHashMap<>();
    if (instrumentation != null) loaderStates(instrumentation.getAllLoadedClasses());
    result.put("installed", installed());
    List<String> active = new ArrayList<>();
    for (ObserverSubscription capture : CAPTURES) if (!active.contains(capture.kind)) active.add(capture.kind);
    result.put("active", active);
    Map<String, Object> hooks = new LinkedHashMap<>();
    for (ObserverAdapter adapter : adapters()) hooks.put(adapter.id(), transformer == null ? 0 : transformer.matched(adapter.id()));
    result.put("hooks", hooks);
    result.put("unmatchedEnds", OPERATIONS.unmatchedEnds());
    result.put("expiredLeases", expiredLeases.get());
    result.put("catalog", OtelTargets.catalog());
    result.put("retiredLoaders", retiredLoaders);
    return result;
  }
  public static byte[] sourceBytes(Class<?> type) throws Exception {
    if (foreignLoader()) return (byte[]) owner().getMethod("sourceBytes", Class.class).invoke(null, type);
    synchronized (MUTATIONS) {
      if (instrumentation == null) throw new IllegalStateException("observer runtime is not installed");
      assertTargetActive(type);
      return transformer.sourceBytes(instrumentation, type);
    }
  }
  static synchronized List<ObserverAdapter> adapters() { return new ArrayList<>(ADAPTERS.values()); }
  static void operation(Map<String, Object> metadata) {
    Object request = metadata.get("request");
    if ("start".equals(metadata.get("phase")) && request != null
        && LoaderLifecycle.retired(request.getClass().getClassLoader()) != null) return;
    for (ObserverAdapter adapter : adapters()) {
      if (!active(adapter)) continue;
      try { adapter.onOperation(metadata); }
      catch (Throwable error) { for (String kind : adapter.kinds()) fail(kind, error); }
    }
  }
  public static Object activeOperation(String signal) { return OPERATIONS.active(signal); }
  static long unmatchedEnds() { return OPERATIONS.unmatchedEnds(); }
  private static final java.util.concurrent.atomic.AtomicLong expiredLeases = new java.util.concurrent.atomic.AtomicLong();
  private static void expire() {
    long now = System.nanoTime();
    for (ObserverSubscription capture : CAPTURES) {
      if (!capture.overdue(now)) continue;
      try { capture.expire(); expiredLeases.incrementAndGet(); }
      catch (Throwable error) { capture.fail(error); }
    }
  }

  private static Object dispatch(String action, Object[] args) {
    switch (action) {
      case "enter": {
        ObserverAdapter adapter;
        synchronized (ObserverRuntime.class) { adapter = ADAPTERS.get(args[0]); }
        if (adapter == null || !active(adapter)) return null;
        if (args[4] != null && LoaderLifecycle.retired(args[4].getClass().getClassLoader()) != null) return null;
        Object[] arguments = (Object[]) args[5], original = arguments.clone();
        try {
          Object token = adapter.onEnter((String) args[1], (String) args[2], (String) args[3], args[4], arguments);
          validateArguments((String) args[3], args[4], original, arguments);
          return token == null ? null : new Call(adapter, token, (String) args[3]);
        } catch (Throwable error) {
          System.arraycopy(original, 0, arguments, 0, original.length);
          for (String kind : adapter.kinds()) fail(kind, error);
          return null;
        }
      }
      case "exit": {
        Call call = (Call) args[0];
        try {
          Object replacement = call.adapter.onExit(call.token, args[1], (Throwable) args[2]);
          if (args[2] != null || compatible(call.descriptor, args[1], replacement)) return replacement;
          throw new IllegalStateException("observer " + call.adapter.id() + " returned an incompatible value for " + call.descriptor);
        } catch (Throwable error) {
          for (String kind : call.adapter.kinds()) fail(kind, error);
          return args[1];
        }
      }
      case "operation": OPERATIONS.observe(args); return null;
      case "failure": fail(null, (Throwable) args[0]); return null;
      default: throw new IllegalArgumentException("unknown observer callback " + action);
    }
  }

  private static boolean active(ObserverAdapter adapter) {
    for (String kind : adapter.kinds()) if (armed(kind)) return true;
    return false;
  }

  public static void assertTargetActive(Class<?> type) { LoaderLifecycle.requireActive(type); }

  private static final class Call {
    final ObserverAdapter adapter;
    final Object token;
    final String descriptor;
    Call(ObserverAdapter adapter, Object token, String descriptor) {
      this.adapter = adapter; this.token = token; this.descriptor = descriptor;
    }
  }

  private static boolean compatible(String descriptor, Object original, Object replacement) {
    org.objectweb.asm.Type result = org.objectweb.asm.Type.getReturnType(descriptor);
    if (result.getSort() == org.objectweb.asm.Type.VOID) return true;
    if (replacement == null) return result.getSort() >= org.objectweb.asm.Type.ARRAY;
    if (result.getSort() < org.objectweb.asm.Type.ARRAY)
      return original != null && original.getClass() == replacement.getClass();
    try {
      ClassLoader loader = original == null ? replacement.getClass().getClassLoader() : original.getClass().getClassLoader();
      String name = result.getSort() == org.objectweb.asm.Type.ARRAY ? result.getDescriptor().replace('/', '.') : result.getClassName();
      return Class.forName(name, false, loader).isInstance(replacement);
    } catch (ClassNotFoundException error) { return false; }
  }

  public static void captureSource(Class<?> type, byte[] bytes) {
    if (foreignLoader()) {
      try { owner().getMethod("captureSource", Class.class, byte[].class).invoke(null, type, bytes); return; }
      catch (ReflectiveOperationException error) { throw new IllegalStateException("capture JVM observer source", error); }
    }
    ObserverTransformer active = transformer;
    if (active != null) active.captureSource(type, bytes);
  }

  private static void validateArguments(String descriptor, Object receiver, Object[] original, Object[] changed)
      throws ClassNotFoundException {
    org.objectweb.asm.Type[] types = org.objectweb.asm.Type.getArgumentTypes(descriptor);
    ClassLoader loader = receiver == null ? Thread.currentThread().getContextClassLoader() : receiver.getClass().getClassLoader();
    for (int i = 0; i < types.length; i++) {
      if (original[i] == changed[i]) continue;
      if (types[i].getSort() < org.objectweb.asm.Type.ARRAY) {
        if (changed[i] != null && original[i] != null && changed[i].getClass() == original[i].getClass()) continue;
      } else if (changed[i] == null) continue;
      else {
        String name = types[i].getSort() == org.objectweb.asm.Type.ARRAY ? types[i].getDescriptor().replace('/', '.') : types[i].getClassName();
        if (Class.forName(name, false, loader).isInstance(changed[i])) continue;
      }
      throw new IllegalStateException("observer replaced argument " + i + " with an incompatible value for " + descriptor);
    }
  }

  private static boolean foreignLoader() {
    return ObserverRuntime.class.getClassLoader() != ClassLoader.getSystemClassLoader();
  }

  private static Class<?> owner() throws ClassNotFoundException {
    return Class.forName(ObserverRuntime.class.getName(), true, ClassLoader.getSystemClassLoader());
  }
}
