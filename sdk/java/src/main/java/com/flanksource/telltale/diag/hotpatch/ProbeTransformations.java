package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.ClassFileTransformer;
import java.lang.instrument.Instrumentation;
import java.lang.instrument.UnmodifiableClassException;
import java.security.ProtectionDomain;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicReference;
import java.util.logging.Level;
import java.util.logging.Logger;
import com.flanksource.telltale.observer.ObserverRuntime;

/** Rebuilds live method probes on the current transformer chain's input. */
public final class ProbeTransformations implements ClassFileTransformer {
  private static final Logger LOG = Logger.getLogger(ProbeTransformations.class.getName());
  private static final Map<Class<?>, Target> TARGETS = Collections.synchronizedMap(new WeakHashMap<>());
  private static Instrumentation instrumentation;
  private static ProbeTransformations transformer;
  private static final InstrumentService INSTRUMENTER = new InstrumentService();
  private static final Map<Class<?>, AtomicReference<byte[]>> READS = new ConcurrentHashMap<>();

  private static synchronized void install(Instrumentation installed) {
    if (instrumentation != null) {
      return;
    }
    if (!installed.isRetransformClassesSupported()) throw new IllegalStateException("method probes require class retransformation");
    SourceTransformations.install(installed);
    transformer = new ProbeTransformations();
    installed.addTransformer(transformer, true);
    instrumentation = installed;
  }

  public static synchronized void shutdown() {
    if (!TARGETS.isEmpty()) throw new IllegalStateException("method probes are still active");
    if (transformer != null && !instrumentation.removeTransformer(transformer))
      throw new IllegalStateException("probe transformer was not registered");
    transformer = null;
    instrumentation = null;
    READS.clear();
  }

  static synchronized InstrumentService.Result arm(Instrumentation installed, ClassIndex.Entry entry, String probeKey) {
    if (!TransformOwner.owns(ProbeTransformations.class)) {
      @SuppressWarnings("unchecked") Map<String, Object> result = (Map<String, Object>) TransformOwner.invoke(
          ProbeTransformations.class, "arm", new Object[] {installed, entry.getType(), probeKey});
      return importResult(result);
    }
    ObserverRuntime.assertTargetActive(entry.getType());
    if (!installed.isModifiableClass(entry.getType())) throw new IllegalStateException("method probe target is not modifiable: " + entry.getName());
    install(installed);
    Target target = new Target(AgentBridge.probeArmingsOwnedBy(entry.getLoaderHash(), entry.getName()), probeKey);
    Target previous = TARGETS.put(entry.getType(), target);
    try {
      retransform(entry.getType());
      if (target.failure != null) throw new IllegalStateException("cannot rebuild method probes on " + entry.getName(), target.failure);
      if (target.result == null) throw new IllegalStateException("the agent has no arming for probe " + probeKey + " on "
          + entry.getName() + " immediately after opening it, so the rewrite could not be built."
          + " The probe was closed underneath this request — a lapsed lease or a restore.");
      target.armed = true;
      return target.result;
    } catch (RuntimeException | LinkageError error) {
      // Put back what every other span armed: the failed rebuild left the class without any probe.
      if (previous == null) TARGETS.remove(entry.getType()); else TARGETS.put(entry.getType(), previous);
      try {
        retransform(entry.getType());
      } catch (RuntimeException | LinkageError rearm) {
        error.addSuppressed(rearm);
      }
      throw error;
    }
  }

  static boolean contains(Class<?> type) {
    if (!TransformOwner.owns(ProbeTransformations.class))
      return Boolean.TRUE.equals(TransformOwner.invoke(ProbeTransformations.class, "contains", new Object[] {type}));
    return TARGETS.containsKey(type);
  }

  static List<Class<?>> types() {
    if (!TransformOwner.owns(ProbeTransformations.class)) {
      @SuppressWarnings("unchecked") List<Class<?>> result = (List<Class<?>>) TransformOwner.invoke(ProbeTransformations.class, "types", new Object[0]);
      return result;
    }
    synchronized (TARGETS) { return new ArrayList<>(TARGETS.keySet()); }
  }

  static byte[] sourceBytes(Class<?> type) {
    if (!TransformOwner.owns(ProbeTransformations.class))
      return (byte[]) TransformOwner.invoke(ProbeTransformations.class, "source", new Object[] {type});
    AtomicReference<byte[]> read = new AtomicReference<>();
    if (READS.putIfAbsent(type, read) != null) throw new IllegalStateException("concurrent source read for " + type.getName());
    try {
      retransform(type);
      if (read.get() == null) throw new IllegalStateException("no probe source bytes for " + type.getName());
      return read.get();
    } finally { READS.remove(type); }
  }

  static synchronized void restore(Class<?> type) {
    if (!TransformOwner.owns(ProbeTransformations.class)) {
      TransformOwner.invoke(ProbeTransformations.class, "restore", new Object[] {type}); return;
    }
    ObserverRuntime.assertTargetActive(type);
    TARGETS.remove(type);
    retransform(type);
  }

  public static Object dispatch(String operation, Object[] args) {
    switch (operation) {
      case "arm": return exportResult(arm((Instrumentation) args[0], ClassIndex.Entry.of((Class<?>) args[1]), (String) args[2]));
      case "contains": return contains((Class<?>) args[0]);
      case "types": return types();
      case "source": return sourceBytes((Class<?>) args[0]);
      case "restore": restore((Class<?>) args[0]); return null;
      default: throw new IllegalArgumentException("unknown probe transformation operation " + operation);
    }
  }

  private static Map<String, Object> exportResult(InstrumentService.Result result) {
    List<Map<String, Object>> matches = new ArrayList<>();
    for (MethodSelector.Match match : result.getMatched()) {
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", match.getMethod().getName()); row.put("descriptor", match.getMethod().getDescriptor());
      row.put("skipped", match.getSkipped()); matches.add(row);
    }
    Map<String, Object> exported = new LinkedHashMap<>();
    exported.put("bytecode", result.getBytecode()); exported.put("matches", matches);
    return exported;
  }

  @SuppressWarnings("unchecked")
  private static InstrumentService.Result importResult(Map<String, Object> result) {
    List<MethodSelector.Match> matches = new ArrayList<>();
    for (Map<String, Object> row : (List<Map<String, Object>>) result.get("matches"))
      matches.add(new MethodSelector.Match(new org.objectweb.asm.commons.Method((String) row.get("name"), (String) row.get("descriptor")), (String) row.get("skipped")));
    return new InstrumentService.Result(matches, (byte[]) result.get("bytecode"));
  }

  private static void retransform(Class<?> type) {
    try { instrumentation.retransformClasses(type); }
    catch (UnmodifiableClassException error) { throw new IllegalStateException("cannot retransform method probes on " + type.getName(), error); }
  }

  public byte[] transform(ClassLoader loader, String name, Class<?> type, ProtectionDomain domain, byte[] bytes) {
    if (type != null) {
      AtomicReference<byte[]> read = READS.get(type);
      if (read != null) read.compareAndSet(null, bytes.clone());
      ObserverRuntime.captureSource(type, bytes);
    }
    Target target = type == null ? null : TARGETS.get(type);
    if (target == null) return null;
    try {
      for (Map<String, Object> armed : target.armings) {
        InstrumentService.Result result = INSTRUMENTER.instrument(request(type.getName(), loader, bytes, armed));
        bytes = result.getBytecode();
        if (target.probeKey.equals(armed.get("key"))) target.result = result;
      }
      return bytes;
    } catch (Throwable error) {
      target.failure = error;
      // The JVM discards what a transformer throws. During an arm the failure reaches the caller
      // through the target; a later rebuild by anyone else has no caller, so it is logged rather
      // than leaving the spans silently recording nothing.
      if (target.armed) LOG.log(Level.WARNING, "telltale-diag: could not re-apply the probes armed on " + type.getName()
          + " while it was retransformed; they record nothing until the class is armed again", error);
      return null;
    }
  }

  private static InstrumentService.Request request(String name, ClassLoader loader, byte[] bytes, Map<String, Object> armed) {
    Object selector = armed.get("method");
    if (!(selector instanceof String)) throw new IllegalStateException("method probe " + armed.get("key") + " has no selector");
    return new InstrumentService.Request(name, bytes, loader, MethodSelector.parse((String) selector), String.valueOf(armed.get("key")))
        .withArgs(Boolean.TRUE.equals(armed.get("args")))
        .withReturn(Boolean.TRUE.equals(armed.get("return")))
        .withException(Boolean.TRUE.equals(armed.get("exception")))
        .withCalls(Boolean.TRUE.equals(armed.get("calls")))
        .withPlatformCalls(Boolean.TRUE.equals(armed.get("platform")));
  }

  private static final class Target {
    final List<Map<String, Object>> armings;
    final String probeKey;
    volatile InstrumentService.Result result;
    volatile Throwable failure;
    volatile boolean armed;
    Target(List<Map<String, Object>> armings, String probeKey) {
      this.armings = new ArrayList<>(armings); this.probeKey = probeKey;
    }
  }
}
