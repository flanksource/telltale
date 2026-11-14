package com.flanksource.telltale.diag.hotpatch;

import java.io.IOException;
import java.lang.instrument.Instrumentation;
import java.util.*;

public final class ClassOperations {
  private final DecompilerService decompiler = new DecompilerService();
  private final CompilerService compiler = new CompilerService();
  private final MethodPatchService methodPatches = new MethodPatchService();
  private final InstrumentService instrumenter = new InstrumentService();

  public synchronized Map<String, Object> compile(JsonBody body) throws IOException {
    String className = body.required("class");
    body.required("source");
    ClassIndex.Entry entry = index().resolveForWrite(className, body.string("loader"));
    if (body.string("method") == null) {
      return compiler
          .compile(
              new CompilerService.Request(className, body.required("source"), entry.getType())
                  .withClasspath(body.string("classpath")))
          .toMap();
    }
    return compileMethod(body, className, entry).toMap();
  }

  public synchronized Map<String, Object> patch(JsonBody body) throws IOException {
    String className = body.required("class");
    ClassIndex index = index();
    ClassIndex.Entry entry = index.resolveForWrite(className, body.string("loader"));
    return body.string("method") == null
        ? patchWholeClass(body, className, index, entry)
        : patchMethod(body, className, index, entry);
  }

  private Map<String, Object> patchWholeClass(
      JsonBody body, String className, ClassIndex index, ClassIndex.Entry entry) {
    CompilerService.Result compiled =
        compiler.compile(
            new CompilerService.Request(className, body.required("source"), entry.getType())
                .withClasspath(body.string("classpath")));
    Map<String, Object> out = new LinkedHashMap<>(compiled.toMap());
    if (!compiled.isSuccess()) {
      out.put("applied", false);
      return out;
    }
    registerLease(body, entry, className);
    out.putAll(patchService().apply(index, entry, compiled.getClasses()));
    out.put("applied", true);
    return out;
  }

  private Map<String, Object> patchMethod(
      JsonBody body, String className, ClassIndex index, ClassIndex.Entry entry) {
    MethodPatchService.Result compiled = compileMethod(body, className, entry);
    Map<String, Object> out = new LinkedHashMap<>(compiled.toMap());
    out.put("method", compiled.getDescriptor());
    if (!compiled.isSuccess()) {
      out.put("applied", false);
      return out;
    }
    byte[] patched = methodPatches.splice(className, compiled, entry.getType().getClassLoader());
    registerLease(body, entry, className);
    out.putAll(patchService().apply(index, entry, Collections.singletonMap(className, patched)));
    out.put("applied", true);
    return out;
  }

  private MethodPatchService.Result compileMethod(
      JsonBody body, String className, ClassIndex.Entry entry) {
    Class<?> target = liveClass(entry, className);
    return methodPatches.compile(
        className,
        target,
        liveBytecode(target),
        MethodSelector.parse(body.required("method")),
        body.required("source"),
        body.string("classpath"));
  }

  private static void registerLease(JsonBody body, ClassIndex.Entry entry, String className) {
    int seconds = body.signedInteger("leaseSeconds");
    if (seconds < 0) {
      return;
    }
    AgentBridge.lease(entry.getLoaderHash(), className, seconds);
  }

  public synchronized Map<String, Object> instrument(JsonBody body) throws IOException {
    String className = body.required("class");
    ClassIndex index = index();
    ClassIndex.Entry entry = index.resolveForWrite(className, body.string("loader"));
    Class<?> target = liveClass(entry, className);
    JsonBody probe = body.object("probe");
    requireAgentVisibleTo(target);
    AgentBridge.requireProbeApi();
    if (Boolean.TRUE.equals(probe.fields().get("alloc"))) {
      AgentBridge.requireProbeApiFor("alloc", AgentBridge.ALLOC_PROBE_API);
    }
    if (Boolean.TRUE.equals(probe.fields().get("gc"))) {
      AgentBridge.requireProbeApiFor("gc", AgentBridge.GC_PROBE_API);
    }

    String probeKey = probe.required("key");
    String selector = body.required("method");
    MethodSelector.parse(selector);
    registerLease(body, entry, className);
    Map<String, Object> options = new LinkedHashMap<>(probe.fields());
    options.put("method", selector);
    Map<String, Object> opened =
        AgentBridge.openProbe(probeKey, entry.getLoaderHash(), className, options);
    if (!Boolean.TRUE.equals(opened.get("opened"))) {
      throw new IllegalStateException("cannot record " + className + ": " + opened.get("error"));
    }

    Map<String, Object> out;
    InstrumentService.Result rewritten;
    try {
      rewritten = armEveryOpenProbe(entry, target, className, probeKey);
      out =
          new LinkedHashMap<>(
              patchService()
                  .apply(
                      index, entry, Collections.singletonMap(className, rewritten.getBytecode())));
    } catch (RuntimeException e) {
      AgentBridge.releaseProbe(probeKey, entry.getLoaderHash(), className);
      throw e;
    }
    out.put("methods", methodRows(rewritten));
    out.put("probeKey", probeKey);
    out.put("generation", opened.get("generation"));
    out.put("capacity", opened.get("capacity"));
    out.put("alloc", Boolean.TRUE.equals(opened.get("alloc")));
    out.put("gc", Boolean.TRUE.equals(opened.get("gc")));
    out.put("instrumented", Boolean.TRUE.equals(out.get("patched")));
    return out;
  }

  private InstrumentService.Result armEveryOpenProbe(
      ClassIndex.Entry entry, Class<?> target, String className, String probeKey) {
    byte[] bytecode = patchService().originalOf(entry);
    if (bytecode == null) {
      bytecode = liveBytecode(target);
    }
    InstrumentService.Result arming = null;
    for (Map<String, Object> armed :
        AgentBridge.probeArmingsOwnedBy(entry.getLoaderHash(), className)) {
      InstrumentService.Result result =
          instrumenter.instrument(armingRequest(className, bytecode, target, armed));
      bytecode = result.getBytecode();
      if (probeKey.equals(armed.get("key"))) {
        arming = result;
      }
    }
    if (arming == null) {
      throw new IllegalStateException(
          "the agent has no arming for probe "
              + probeKey
              + " on "
              + className
              + " immediately after opening it, so the rewrite could not be built."
              + " The probe was closed underneath this request — a lapsed lease or a restore.");
    }
    return arming;
  }

  private static InstrumentService.Request armingRequest(
      String className, byte[] bytecode, Class<?> target, Map<String, Object> armed) {
    Object selector = armed.get("method");
    if (!(selector instanceof String)) {
      throw new IllegalStateException(
          "probe "
              + armed.get("key")
              + " is recording on "
              + className
              + " but the agent has no selector for it, so the class cannot be rewritten without"
              + " disarming it. It was armed by an older WAR; restore "
              + className
              + " and arm the spans again.");
    }
    return new InstrumentService.Request(
            className,
            bytecode,
            target.getClassLoader(),
            MethodSelector.parse((String) selector),
            String.valueOf(armed.get("key")))
        .withArgs(Boolean.TRUE.equals(armed.get("args")))
        .withReturn(Boolean.TRUE.equals(armed.get("return")))
        .withException(Boolean.TRUE.equals(armed.get("exception")))
        .withCalls(Boolean.TRUE.equals(armed.get("calls")))
        .withPlatformCalls(Boolean.TRUE.equals(armed.get("platform")));
  }

  private static List<Map<String, Object>> methodRows(InstrumentService.Result rewritten) {
    List<Map<String, Object>> rows = new ArrayList<>();
    for (MethodSelector.Match match : rewritten.getMatched()) {
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", match.getMethod().getName());
      row.put("descriptor", match.getMethod().getDescriptor());
      row.put("instrumented", match.isInstrumentable());
      if (!match.isInstrumentable()) {
        row.put("skipped", match.getSkipped());
      }
      rows.add(row);
    }
    return rows;
  }

  public synchronized Map<String, Object> restore(JsonBody body) throws IOException {
    String className = body.required("class");
    ClassIndex index = index();
    ClassIndex.Entry entry = index.resolveForWrite(className, body.string("loader"));
    String loaderHash = entry.getLoaderHash();
    String probeKey = body.optionalObject("probe").string("key");

    if (probeKey != null) {
      Map<String, Object> released = AgentBridge.releaseProbe(probeKey, loaderHash, className);
      List<String> stillRecording = AgentBridge.probeKeysOwnedBy(loaderHash, className);
      if (!stillRecording.isEmpty()) {
        Map<String, Object> held =
            new LinkedHashMap<>(patchService().status(index, loaderHash, className));
        held.put("restored", Collections.emptyList());
        held.put("released", probeKey);
        held.put("heldBy", stillRecording);
        held.put("probesClosed", probesClosed(released));
        return held;
      }
      Map<String, Object> out = new LinkedHashMap<>(patchService().restore(index, entry));
      out.put("released", probeKey);
      out.put("heldBy", Collections.emptyList());
      out.put("probesClosed", probesClosed(released));
      return out;
    }

    Map<String, Object> out = new LinkedHashMap<>(patchService().restore(index, entry));
    out.put("probesClosed", AgentBridge.closeProbesOwnedBy(loaderHash, className));
    return out;
  }

  private static List<Map<String, Object>> probesClosed(Map<String, Object> released) {
    if (!Boolean.TRUE.equals(released.get("closed"))) {
      return Collections.emptyList();
    }
    return Collections.singletonList(released);
  }

  public synchronized Map<String, Object> lease(JsonBody body) throws IOException {
    String className = body.required("class");
    ClassIndex.Entry entry = index().resolveForWrite(className, body.string("loader"));
    AgentBridge.lease(entry.getLoaderHash(), className, body.integer("seconds"));
    return patchService().heldStatus(entry);
  }

  private static final int MAX_PROBE_LIMIT = 10_000;
  private static final int DEFAULT_PROBE_LIMIT = 1_000;

  public Map<String, Object> probes(JsonBody body) {
    String key = body.string("key");
    if (key == null) {
      throw new IllegalArgumentException("key is required");
    }
    if (body.string("drain") != null) {
      throw new IllegalArgumentException(
          "drain= was replaced by since=: a read no longer removes"
              + " what it returns, and reports what the ring evicted instead");
    }
    return AgentBridge.drain(key, cursor(body), limit(body));
  }

  public Map<String, Object> freezeProbe(JsonBody body) throws IOException {
    return AgentBridge.freezeProbe(body.required("key"));
  }

  public Map<String, Object> probeKeys() {
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("keys", AgentBridge.probeKeys());
    return out;
  }

  static long cursor(JsonBody body) {
    Object since = body.fields().get("since");
    if (since == null) {
      return 0L;
    }
    try {
      if (!(since instanceof Number))
        throw new IllegalArgumentException("since must be an integer");
      long parsed = new java.math.BigDecimal(since.toString()).longValueExact();
      if (parsed < 0L) {
        throw new IllegalArgumentException("since must not be negative, got " + since);
      }
      return parsed;
    } catch (ArithmeticException | NumberFormatException e) {
      throw new IllegalArgumentException("since must be a sequence number, got " + since, e);
    }
  }

  private static int limit(JsonBody body) {
    if (body.fields().get("limit") == null) {
      return DEFAULT_PROBE_LIMIT;
    }
    int parsed = body.integer("limit");
    if (parsed <= 0) {
      throw new IllegalArgumentException("limit must be positive, got " + parsed);
    }
    return Math.min(parsed, MAX_PROBE_LIMIT);
  }

  private static void requireAgentVisibleTo(Class<?> target) {
    try {
      Class.forName(InstrumentationService.AGENT_CLASS, false, target.getClassLoader());
    } catch (ClassNotFoundException e) {
      throw new IllegalStateException(
          target.getName()
              + " is defined by a class loader that cannot see "
              + InstrumentationService.AGENT_CLASS
              + " on the system class path, so instrumented code"
              + " would fail with NoClassDefFoundError the first time the method ran",
          e);
    }
  }

  private static byte[] liveBytecode(Class<?> target) {
    return new BytecodeSource(InstrumentationService.instrumentation()).bytecodeOf(target);
  }

  private static Class<?> liveClass(ClassIndex.Entry entry, String className) {
    Class<?> type = entry.getType();
    if (type == null) {
      throw new IllegalStateException("no live Class handle for " + className);
    }
    return type;
  }

  private static PatchService patchService() {
    return new PatchService(InstrumentationService.instrumentation());
  }

  public synchronized Map<String, Object> source(JsonBody body) {
    String className = body.string("class");
    if (className == null) {
      throw new IllegalArgumentException("class is required");
    }
    ClassIndex index = index();
    ClassIndex.Entry entry = index.resolve(className, loader(body, index));
    Class<?> type = liveClass(entry, className);

    BytecodeSource bytecode = new BytecodeSource(InstrumentationService.instrumentation());
    byte[] bytes = bytecode.bytecodeOf(type);
    String digest = BytecodeSource.digest(bytes);
    DecompilerService.Request request =
        new DecompilerService.Request(className, digest, bytes)
            .withMethodName(body.string("method"));
    for (ClassIndex.Entry inner : index.innerClasses(entry)) {
      if (inner.getType() == null) {
        continue;
      }
      try {
        request.withInnerClass(inner.getName(), bytecode.bytecodeOf(inner.getType()));
      } catch (IllegalStateException e) {
        continue;
      }
    }

    DecompilerService.Result result = decompiler.decompile(request);
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("class", className);
    out.put("loader", entry.getLoaderHash());
    out.put("source", result.getSource());
    out.put("methods", result.getMethods());
    out.put("sourceLineNumbers", result.getSourceLineNumbers());
    out.put("lineRows", result.getLineRows());
    out.put("digest", digest);
    out.put("decompiler", DecompilerService.NAME);
    out.put("durationMs", result.getDurationMs());
    out.put("cached", result.isCached());
    return out;
  }

  public synchronized byte[] bytecode(JsonBody body) {
    String className = body.string("class");
    if (className == null) {
      throw new IllegalArgumentException("class is required");
    }
    ClassIndex index = index();
    return liveBytecode(liveClass(index.resolve(className, loader(body, index)), className));
  }

  private static ClassIndex index() {
    Instrumentation instrumentation = InstrumentationService.instrumentation();
    return ClassIndex.from(instrumentation);
  }

  private static String loader(JsonBody body, ClassIndex index) {
    String loader = body.string("loader");
    if (loader != null && !index.hasLoader(loader)) {
      throw new NoSuchElementException("loader not found: " + loader);
    }
    return loader;
  }
}
