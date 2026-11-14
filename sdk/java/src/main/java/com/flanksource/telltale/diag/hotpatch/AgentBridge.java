package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.Instrumentation;
import java.lang.reflect.Field;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Reaches the probe buffer and the patch leases that live in the attached agent, on the system
 * class path, from this WAR.
 *
 * <p>They live there rather than here for the reason the pre-patch byte stash does: a redeployed
 * WAR gets a fresh classloader, while the classes it rewrote stay rewritten. State that can undo a
 * patch must outlive the thing that made it. Instrumented bytecode also calls the agent by name,
 * and only the system class path is visible from every loader that might define a patched class.
 *
 * <p>Everything crossing the boundary is a JDK type — {@code Map}, {@code List}, {@code String},
 * {@code byte[]} — so neither side needs the other's classes. The one exception is the restore
 * callback, which is an interface declared in the agent; it is implemented with a {@link Proxy}
 * defined by the agent's own loader, since this WAR cannot name a type it cannot see.
 */
public final class AgentBridge {

  private static final Logger LOG = Logger.getLogger(AgentBridge.class.getName());

  /** Default lease when a caller does not choose one; see {@code /classes/lease}. */
  public static final int DEFAULT_LEASE_SECONDS = 120;

  private static volatile boolean restorerInstalled;

  private AgentBridge() {}

  /** The probe API generation the bytecode this WAR injects depends on. */
  public static final int REQUIRED_PROBE_API = 7;

  /**
   * Refuses to instrument when the attached agent predates the probe calls this WAR is about to
   * inject into application code.
   *
   * <p>This check is the difference between a clear refusal and the worst failure this subsystem
   * can produce. The agent jar sits on the system class path and cannot be reloaded without
   * restarting the JVM, so a pod that attached an older agent and then received a newer WAR would
   * happily redefine a class whose new bytecode calls a method that is not there — and the {@link
   * NoSuchMethodError} would be raised inside application's own call path, on a request that never
   * asked to be traced. Checking that the class merely <em>loads</em> is not enough; the injected
   * signatures have to be there.
   */
  public static void requireProbeApi() {
    Class<?> agent = agent();
    int version = probeApiVersion(agent);
    if (version < REQUIRED_PROBE_API) {
      throw new IllegalStateException(
          staleAgent(
              "it implements probe API v"
                  + version
                  + " while this WAR injects calls from v"
                  + REQUIRED_PROBE_API));
    }
    requireMethod(
        agent,
        "probeEnter",
        String.class,
        String.class,
        String.class,
        String.class,
        int.class,
        Object[].class);
    requireMethod(agent, "probeReturn", String.class, long.class, Object.class);
    requireMethod(agent, "probeThrow", String.class, long.class, Throwable.class);
    requireMethod(agent, "probeExit", String.class, long.class);
    // Not injected — the WAR calls these itself, at restore. They are checked
    // HERE anyway, because the alternative is discovering the agent cannot
    // release a probe at the moment the class is already patched and this is
    // the call that would put it back.
    requireMethod(agent, "releaseProbe", String.class, String.class);
    requireMethod(agent, "probeKeysOwnedBy", String.class);
    // Also not injected, and checked here for the mirror-image reason: a WAR
    // that could not read back what an earlier span armed would rebuild the
    // class from its own selector alone and disarm that span, then report a
    // clean capture of nothing.
    requireMethod(agent, "probeArmingsOwnedBy", String.class);
  }

  /** The probe API generation that measures {@code alloc}; see the agent's PROBE_API_VERSION. */
  public static final int ALLOC_PROBE_API = 8;

  /** The probe API generation that measures {@code gc}. */
  public static final int GC_PROBE_API = 9;

  /**
   * Refuses an opt-in probe {@code option} when the attached agent predates {@code version}, the
   * generation that implements it.
   *
   * <p>Checked only for a span that asks, unlike {@link #requireProbeApi}: no injected call
   * changed, so an older agent still serves every other capture. It would ignore the option as
   * unknown, though, and report a capture without the measurement and no reason — which is what
   * this refuses.
   */
  public static void requireProbeApiFor(String option, int version) {
    int attached = probeApiVersion(agent());
    if (attached < version) {
      throw new IllegalStateException(
          option
              + " needs probe API v"
              + version
              + " and the agent"
              + " attached to this JVM implements v"
              + attached
              + ". The agent jar is on the system class"
              + " path and cannot be replaced while the JVM runs; restart the JVM to measure "
              + option
              + ", or trace without it.");
    }
  }

  private static int probeApiVersion(Class<?> agent) {
    try {
      return ((Number) agent.getMethod("probeApiVersion").invoke(null)).intValue();
    } catch (ReflectiveOperationException | ClassCastException e) {
      throw new IllegalStateException(staleAgent("it has no probeApiVersion()"), e);
    }
  }

  private static void requireMethod(Class<?> agent, String name, Class<?>... types) {
    try {
      agent.getMethod(name, types);
    } catch (NoSuchMethodException e) {
      throw new IllegalStateException(
          staleAgent("it has no " + name + " with the signature this WAR uses"), e);
    }
  }

  private static String staleAgent(String because) {
    return "the agent attached to this JVM predates the probe API this WAR uses ("
        + because
        + "). The agent jar is on the system class path and cannot be replaced while the JVM"
        + " runs, so instrumenting now would leave application code calling a method that is"
        + " not there. Restart the JVM to pick up the current agent.";
  }

  /**
   * Opens the buffer an instrumented method records into.
   *
   * <p>Called before the class is rewritten, so a refusal — the JVM already has as many probes open
   * as it allows — reaches the caller while the JVM is still untouched, rather than after a
   * redefinition that would record into nothing.
   *
   * <p>The probe is owned by the class it instruments, keyed the way the agent already keys the
   * byte stash and the leases, so a restore or a lapsed lease closes it without needing a second
   * registry to consult.
   */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> openProbe(
      String key, String loaderHash, String className, Map<String, Object> options) {
    InstrumentService.validateProbeKey(key);
    return (Map<String, Object>)
        invoke(
            "openProbe",
            new Class<?>[] {String.class, String.class, Map.class},
            key,
            PatchService.key(loaderHash, className),
            options);
  }

  /** Closes one probe and releases its buffer. */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> closeProbe(String key) {
    InstrumentService.validateProbeKey(key);
    return (Map<String, Object>) invoke("closeProbe", new Class<?>[] {String.class}, key);
  }

  /** Stops new probe events while retaining the ring for a final cursor read. */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> freezeProbe(String key) {
    InstrumentService.validateProbeKey(key);
    return (Map<String, Object>) invoke("freezeProbe", new Class<?>[] {String.class}, key);
  }

  /**
   * Closes every probe opened for one class.
   *
   * <p>Called from the restore path and from the lease reaper. Without it a killed client leaks its
   * buffer for the life of the JVM: probe keys carry a random per-span suffix, so each abandoned
   * span strands a distinct key that nothing will ever look up again.
   */
  @SuppressWarnings("unchecked")
  public static List<Map<String, Object>> closeProbesOwnedBy(String loaderHash, String className) {
    return (List<Map<String, Object>>)
        invoke(
            "closeProbesOwnedBy",
            new Class<?>[] {String.class},
            PatchService.key(loaderHash, className));
  }

  /**
   * Releases one span's claim on a class, closing its ring if that was the last class the span
   * recorded for.
   *
   * <p>The keyed counterpart of {@link #closeProbesOwnedBy}. A restore that knows which span is
   * finishing must use this: several spans routinely instrument ONE class — the preservation plan
   * arms {@code ActivityDal} three ways in a single step — and releasing by class alone closes all
   * of their rings at once, so the first span to finish silently disarms the rest.
   */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> releaseProbe(String key, String loaderHash, String className) {
    InstrumentService.validateProbeKey(key);
    return (Map<String, Object>)
        invoke(
            "releaseProbe",
            new Class<?>[] {String.class, String.class},
            key,
            PatchService.key(loaderHash, className));
  }

  /**
   * The probe keys still recording on one class.
   *
   * <p>A class's original bytes are shared by every span instrumented into it, so this is the
   * question a restore asks before putting them back: an empty list means the restore is this
   * span's alone to make.
   */
  @SuppressWarnings("unchecked")
  public static List<String> probeKeysOwnedBy(String loaderHash, String className) {
    return (List<String>)
        invoke(
            "probeKeysOwnedBy",
            new Class<?>[] {String.class},
            PatchService.key(loaderHash, className));
  }

  /**
   * Everything still armed into one class, oldest arming first.
   *
   * <p>What a NEW arming has to read before it rewrites the class, because a class is redefined
   * whole: the bytes it hands the JVM replace every earlier span's rewritten methods along with its
   * own, so it must carry all of them. Each row is the options the probe was opened with — the
   * selector included — plus the {@code key} it records under.
   *
   * <p>Only armings of rings that still claim the class come back. The agent keeps every arming a
   * ring was opened with, including one whose class was since released while another class kept the
   * ring open — a multi-target span releases its classes one at a time. Rebuilding the class from
   * that stale arming would re-wrap a released selector and record it into a ring that no longer
   * counts the class as an owner, so nothing would ever restore it on that ring's behalf.
   */
  @SuppressWarnings("unchecked")
  public static List<Map<String, Object>> probeArmingsOwnedBy(String loaderHash, String className) {
    List<Map<String, Object>> armings =
        (List<Map<String, Object>>)
            invoke(
                "probeArmingsOwnedBy",
                new Class<?>[] {String.class},
                PatchService.key(loaderHash, className));
    List<String> holding = probeKeysOwnedBy(loaderHash, className);
    List<Map<String, Object>> live = new ArrayList<>(armings.size());
    for (Map<String, Object> arming : armings) {
      if (holding.contains(String.valueOf(arming.get("key")))) {
        live.add(arming);
      }
    }
    return live;
  }

  /**
   * Every probe this JVM still has open, each with what its owners were armed for.
   *
   * <p>The ring cannot say whether it records a watch or a trace — that lives in the options it was
   * opened with, which the agent keeps without reading. Interpreting them is this WAR's business
   * (the agent can only be replaced by restarting the JVM), so the join happens here: one {@code
   * armings} row per owner still holding the ring, carrying the selector and {@code calls}, oldest
   * arming first — the owners set has no order of its own.
   */
  @SuppressWarnings("unchecked")
  public static List<Map<String, Object>> probeKeys() {
    List<Map<String, Object>> keys =
        (List<Map<String, Object>>) invoke("probeKeys", new Class<?>[0]);
    Map<String, List<Map<String, Object>>> armingsByOwner = new LinkedHashMap<>();
    for (Map<String, Object> row : keys) {
      String key = String.valueOf(row.get("key"));
      List<Map<String, Object>> armed = new ArrayList<>();
      for (Object owner : (List<Object>) row.get("owners")) {
        List<Map<String, Object>> owned =
            armingsByOwner.computeIfAbsent(
                String.valueOf(owner),
                o ->
                    (List<Map<String, Object>>)
                        invoke("probeArmingsOwnedBy", new Class<?>[] {String.class}, o));
        for (Map<String, Object> arming : owned) {
          if (key.equals(arming.get("key"))) {
            armed.add(arming);
          }
        }
      }
      armed.sort(Comparator.comparingLong(AgentBridge::armedSeq));
      List<Map<String, Object>> armings = new ArrayList<>(armed.size());
      for (Map<String, Object> arming : armed) {
        armings.add(armingRow(key, arming));
      }
      row.put("armings", armings);
    }
    return keys;
  }

  private static long armedSeq(Map<String, Object> arming) {
    Object seq = arming.get("armedSeq");
    if (!(seq instanceof Number)) {
      throw new IllegalStateException("agent reported an arming with no armedSeq: " + arming);
    }
    return ((Number) seq).longValue();
  }

  private static Map<String, Object> armingRow(String key, Map<String, Object> arming) {
    Object owner = arming.get("owner");
    Object method = arming.get("method");
    if (!(method instanceof String) || ((String) method).trim().isEmpty()) {
      throw new IllegalStateException(
          "probe "
              + key
              + " is armed on "
              + owner
              + " without a method selector; every probe the WAR opens records the one it was armed"
              + " for");
    }
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("owner", owner);
    row.put("method", method);
    row.put("calls", Boolean.valueOf(Boolean.TRUE.equals(arming.get("calls"))));
    return row;
  }

  /**
   * Reads the events recorded under {@code key} from {@code sinceSeq} forward, without removing
   * them.
   *
   * <p>A cursor rather than a destructive drain, for two reasons. It lets a following CLI and a
   * browser watch one span at once. And it removes by construction the race the destructive version
   * had — a window of a few instructions in which a probe that had already fetched the buffer
   * appended to the one just removed, and that event went unreported with nothing to show it had
   * ever existed. What bounds a long-lived JVM instead is the ring itself, which is fixed-size and
   * reports what it evicted.
   *
   * <p>The events come back as rendered JSON lines. This WAR forwards them without parsing them:
   * they were serialised inside the JVM that produced them and are appended to the caller's archive
   * verbatim.
   *
   * <p>A read of an open ring also names its {@code owners} — the classes it still records for. A
   * span arming several classes into one ring stays open while another surface restores one of
   * them, and that restore drops the class from the ring without closing it; the owners are how the
   * span learns that one of its targets was revoked while the rest kept recording.
   */
  @SuppressWarnings("unchecked")
  public static Map<String, Object> drain(String key, long sinceSeq, int limit) {
    InstrumentService.validateProbeKey(key);
    Map<String, Object> read =
        (Map<String, Object>)
            invoke(
                "drain",
                new Class<?>[] {String.class, long.class, int.class},
                key,
                Long.valueOf(sinceSeq),
                Integer.valueOf(limit));
    for (Map<String, Object> open :
        (List<Map<String, Object>>) invoke("probeKeys", new Class<?>[0])) {
      if (key.equals(open.get("key"))) {
        read.put("owners", open.get("owners"));
      }
    }
    return read;
  }

  /**
   * When the agent will restore a class on its own, as epoch millis; 0 when no lease is scheduled
   * for it.
   *
   * <p>Zero is the state worth surfacing rather than hiding: a patched class with no lease is one
   * nothing will ever undo, which is exactly what a reader of the patch register needs to be told.
   */
  @SuppressWarnings("unchecked")
  public static long leaseExpiresAt(String loaderHash, String className) {
    Map<String, Long> leases = (Map<String, Long>) field("LEASES");
    Long deadline = leases.get(PatchService.key(loaderHash, className));
    return deadline == null ? 0L : deadline.longValue();
  }

  private static Object field(String name) {
    try {
      Object value = agent().getField(name).get(null);
      if (value == null) {
        throw new IllegalStateException(
            InstrumentationService.AGENT_CLASS
                + "."
                + name
                + " is null — the attached agent jar predates instrumentation; redeploy"
                + " telltale-diag");
      }
      return value;
    } catch (ReflectiveOperationException e) {
      throw new IllegalStateException(staleAgent("it has no " + name + " field"), e);
    }
  }

  private static Object invoke(String name, Class<?>[] types, Object... args) {
    try {
      return agent().getMethod(name, types).invoke(null, args);
    } catch (ReflectiveOperationException e) {
      throw new IllegalStateException(staleAgent("it has no " + name + "()"), e);
    }
  }

  /**
   * Registers or extends the deadline after which the agent puts a class's original bytes back on
   * its own.
   *
   * <p>This is what makes a killed client safe. Every mutation that a span holds open registers a
   * lease and renews it while the span runs; if the process holding it dies — a killed CLI, a
   * dropped pod-exec, a Ctrl-C — nothing renews, and the JVM restores itself rather than serving
   * rewritten code indefinitely.
   */
  public static void lease(String loaderHash, String className, int seconds) {
    installRestorer();
    int effective = seconds > 0 ? seconds : DEFAULT_LEASE_SECONDS;
    try {
      agent()
          .getMethod("lease", String.class, long.class)
          .invoke(null, PatchService.key(loaderHash, className), (long) effective * 1000L);
    } catch (ReflectiveOperationException e) {
      throw new IllegalStateException(
          "the attached agent jar cannot hold a patch lease, so a patch would outlive the run that"
              + " made it. Restart the JVM to pick up the current agent: "
              + e,
          e);
    }
  }

  /**
   * Teaches the agent how to undo a patch, and starts the daemon that does it.
   *
   * <p>The agent owns the deadlines but not the redefinition — it has no {@link ClassIndex} and no
   * {@link PatchService}, and giving it either would put the whole hot-patch machinery on the
   * system class path where a redeploy could never update it. So the WAR supplies the callback and
   * the agent supplies the clock.
   */
  public static synchronized void installRestorer() {
    Class<?> agent = agent();
    try {
      Class<?> restorer =
          Class.forName(agent.getName() + "$Restorer", false, agent.getClassLoader());
      Field field = agent.getField("RESTORER");
      // Installed once per WAR instance, and again whenever the agent has
      // none. The first half matters after a redeploy: a callback from the
      // previous WAR would keep its whole classloader alive. The second is
      // the invariant this method exists for — returning while the agent
      // has no way back would leave a patch that cannot be undone.
      if (restorerInstalled && field.get(null) != null) {
        return;
      }
      field.set(
          null,
          Proxy.newProxyInstance(
              agent.getClassLoader(),
              new Class<?>[] {restorer},
              new InvocationHandler() {
                @Override
                public Object invoke(Object proxy, Method method, Object[] args) throws Throwable {
                  if ("restore".equals(method.getName()) && args != null && args.length == 1) {
                    restoreLeased(String.valueOf(args[0]));
                    return null;
                  }
                  return defaultAnswer(proxy, method, args);
                }
              }));
      agent.getMethod("startReaper").invoke(null);
      restorerInstalled = true;
    } catch (ReflectiveOperationException e) {
      throw new IllegalStateException(
          "the attached agent jar cannot restore a lapsed patch, so a patch would outlive the run"
              + " that made it. Restart the JVM to pick up the current agent: "
              + e,
          e);
    }
  }

  /** Puts one leased class back, keyed the way the agent stores it. */
  static void restoreLeased(String key) {
    int split = key.indexOf('|');
    if (split < 0) {
      throw new IllegalArgumentException("malformed lease key: " + key);
    }
    String loaderHash = key.substring(0, split);
    String className = key.substring(split + 1);
    Instrumentation instrumentation = InstrumentationService.instrumentation();
    ClassIndex index = ClassIndex.from(instrumentation);
    new PatchService(instrumentation).restore(index, index.resolve(className, loaderHash));
    LOG.log(
        Level.WARNING,
        "telltale-diag: restored {0} (loader {1}) — its patch lease lapsed,"
            + " so the client holding it is gone",
        new Object[] {className, loaderHash});
  }

  /** Object's own methods, which a Proxy also routes through the handler. */
  private static Object defaultAnswer(Object proxy, Method method, Object[] args) {
    if ("hashCode".equals(method.getName())) {
      return System.identityHashCode(proxy);
    }
    if ("equals".equals(method.getName())) {
      return proxy == (args == null ? null : args[0]);
    }
    if ("toString".equals(method.getName())) {
      return "telltale-diag lease restorer";
    }
    throw new UnsupportedOperationException(
        "the lease restorer does not implement " + method.getName());
  }

  private static Class<?> agent() {
    try {
      return ClassLoader.getSystemClassLoader().loadClass(InstrumentationService.AGENT_CLASS);
    } catch (ClassNotFoundException e) {
      throw new IllegalStateException(
          "the instrumentation agent is not attached to this JVM: " + e, e);
    }
  }
}
