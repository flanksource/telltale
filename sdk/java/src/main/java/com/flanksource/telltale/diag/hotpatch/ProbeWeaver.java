package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.ClassFileTransformer;
import java.lang.instrument.Instrumentation;
import java.lang.instrument.UnmodifiableClassException;
import java.security.ProtectionDomain;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Puts the probes armed on a class into it, every time the JVM rebuilds it.
 *
 * <p>Instrumenting by {@code redefineClasses} does not last. Once any retransform-capable agent has
 * changed a class — an APM agent loaded through JAVA_TOOL_OPTIONS, or an Arthas {@code watch} —
 * HotSpot caches the class file as it was before that change and starts every later
 * retransformation from the cache, not from the bytes a redefinition installed. So the next
 * retransformation of the class, by anyone, silently put the original bytes back: the ring stayed
 * open, the lease stayed held, and the probe recorded nothing.
 *
 * <p>A retransform-capable transformer is how instrumentation survives that, and it is what Arthas
 * and the OpenTelemetry agent themselves do: the class is armed by a retransformation, and every
 * later one — whoever asks for it — comes through here again and has the probes re-applied to
 * whatever bytes the JVM hands over. Those bytes already carry every earlier agent's changes, so
 * nothing anyone else wove in is lost either.
 *
 * <p>What is armed is read from the agent on every pass ({@code probeArmingsOwnedBy}), never held
 * here: the agent outlives this classloader, and a released probe has to stop being re-applied the
 * moment it is released.
 *
 * <p>Ported from oipa-cli's oipa-diag {@code ProbeWeaver}.
 */
public final class ProbeWeaver implements ClassFileTransformer {

  private static final Logger LOG = Logger.getLogger(ProbeWeaver.class.getName());

  private static ProbeWeaver installed;

  private final Instrumentation instrumentation;
  private final InstrumentService instrumenter = new InstrumentService();

  /**
   * The weave this thread asked for. The JVM discards whatever a transformer throws and keeps the
   * bytes it was given, so the outcome is handed back through here — a refused arm must reach the
   * caller as an error, not as a class that quietly runs uninstrumented.
   */
  private final ThreadLocal<Request> requested = new ThreadLocal<Request>();

  private ProbeWeaver(Instrumentation instrumentation) {
    this.instrumentation = instrumentation;
  }

  /** Registers the weaver once per classloader and returns it. */
  public static synchronized ProbeWeaver install(Instrumentation instrumentation) {
    if (installed == null) {
      ProbeWeaver weaver = new ProbeWeaver(instrumentation);
      instrumentation.addTransformer(weaver, true);
      installed = weaver;
    }
    return installed;
  }

  /**
   * Unregisters the weaver. A registered transformer keeps its classloader alive, and a second
   * loader registering its own would weave every probe twice. Classes armed when it goes keep the
   * bytes they run until something retransforms them.
   */
  public static synchronized void uninstall() {
    if (installed != null) {
      installed.instrumentation.removeTransformer(installed);
      installed = null;
    }
  }

  /**
   * Rebuilds {@code target} with every probe still armed on it.
   *
   * @param probeKey the probe whose pass the caller reports, or null when the caller only needs the
   *     class to match what is armed.
   * @return what was woven; null only when nothing is armed and probeKey is null.
   */
  public Woven weave(Class<?> target, String probeKey) {
    Request request = new Request(target);
    requested.set(request);
    try {
      instrumentation.retransformClasses(target);
    } catch (UnmodifiableClassException e) {
      throw new IllegalStateException(
          target.getName() + " cannot be retransformed, so it cannot be instrumented: " + e, e);
    } finally {
      requested.remove();
    }
    if (request.failure != null) {
      throw new IllegalStateException(
          "instrumenting " + target.getName() + " failed: " + request.failure.getMessage(),
          request.failure);
    }
    if (!request.reached) {
      throw new IllegalStateException(
          "retransforming "
              + target.getName()
              + " never reached the probe weaver, so nothing was instrumented");
    }
    if (probeKey != null && (request.woven == null || request.woven.arming(probeKey) == null)) {
      throw new IllegalStateException(
          "the agent has no arming for probe "
              + probeKey
              + " on "
              + target.getName()
              + " immediately after opening it, so the rewrite could not be built."
              + " The probe was closed underneath this request — a lapsed lease or a restore.");
    }
    return request.woven;
  }

  @Override
  public byte[] transform(
      ClassLoader loader,
      String internalName,
      Class<?> redefined,
      ProtectionDomain domain,
      byte[] buffer) {
    if (redefined == null) {
      // A class being loaded for the first time: nothing can be armed on it.
      return null;
    }
    Request request = requested.get();
    if (request != null && request.target == redefined) {
      request.reached = true;
    } else {
      request = null;
    }
    try {
      Woven woven = rebuild(redefined, buffer);
      if (request != null) {
        request.woven = woven;
      }
      return woven == null ? null : woven.bytecode;
    } catch (RuntimeException | LinkageError e) {
      if (request != null) {
        request.failure = e;
      } else {
        LOG.log(
            Level.WARNING,
            "telltale-diag: could not re-apply the probes armed on "
                + redefined.getName()
                + " while it was retransformed; they record nothing until the class is armed again",
            e);
      }
      return null;
    }
  }

  /**
   * Applies every arming in the order it was armed, each pass on the output of the one before —
   * two spans on ONE method nest, and the order has to be the same every time the class is rebuilt.
   */
  private Woven rebuild(Class<?> type, byte[] bytecode) {
    String className = type.getName();
    List<Map<String, Object>> armings =
        AgentBridge.probeArmingsOwnedBy(ClassIndex.hash(type.getClassLoader()), className);
    if (armings.isEmpty()) {
      return null;
    }
    Map<String, InstrumentService.Result> passes =
        new LinkedHashMap<String, InstrumentService.Result>();
    byte[] woven = bytecode;
    for (Map<String, Object> armed : armings) {
      InstrumentService.Result pass =
          instrumenter.instrument(armingRequest(className, woven, type, armed));
      woven = pass.getBytecode();
      passes.put(String.valueOf(armed.get("key")), pass);
    }
    return new Woven(woven, passes);
  }

  /** One open probe's arming, back in the shape that produced it. */
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
              + " disarming it. It was armed by an older agent; restore "
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

  /** The bytes a weave installed, and each probe's own pass over them. */
  public static final class Woven {
    private final byte[] bytecode;
    private final Map<String, InstrumentService.Result> passes;

    Woven(byte[] bytecode, Map<String, InstrumentService.Result> passes) {
      this.bytecode = bytecode;
      this.passes = passes;
    }

    public byte[] getBytecode() {
      return bytecode;
    }

    /** The pass that armed {@code probeKey}. */
    public InstrumentService.Result arming(String probeKey) {
      return passes.get(probeKey);
    }
  }

  private static final class Request {
    final Class<?> target;
    boolean reached;
    Woven woven;
    Throwable failure;

    Request(Class<?> target) {
      this.target = target;
    }
  }
}
