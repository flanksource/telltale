package com.flanksource.telltale.diag.agent;

import com.flanksource.telltale.diag.graph.InertTypes;
import com.flanksource.telltale.diag.graph.ValueGraph;
import java.lang.instrument.Instrumentation;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Java agent holding the JVM's {@link Instrumentation} handle, the pre-patch bytes of every class
 * the WAR has rewritten, and the events instrumented methods record.
 *
 * <p>This class ships in its own jar (the {@code agentJar} task) which {@code
 * VirtualMachine.loadAgent} appends to the <em>system</em> class path. Two things follow, and
 * between them they explain every design choice in here:
 *
 * <ul>
 *   <li>It outlives a WAR redeploy, which is why the byte stash and the probe buffers live here and
 *       not in the webapp — a redeployed WAR gets a fresh classloader and would lose them while the
 *       classes it patched stay patched.
 *   <li>It is reachable from every classloader in the JVM, which is what lets instrumented
 *       application code call {@link #probeEnter} by name.
 * </ul>
 *
 * <p>The methods injected into application code — {@code probeEnter}, {@code probeReturn}, {@code
 * probeThrow}, {@code probeExit} — run inside code that never asked to be observed. They catch
 * {@link Throwable}, they do not block, they run none of the application's code, and they allocate
 * no more than the event and its bounded value snapshot. Observation must not break the observed.
 *
 * <p>The class initialiser is held to the same bar for the same reason: it runs on whichever
 * application thread first reaches an instrumented method, so a failure there would be an {@code
 * ExceptionInInitializerError} in application's own call path, permanent until the JVM restarts. It
 * therefore reads no system properties (a {@code SecurityManager} can deny that), does no I/O, and
 * logs nothing. Configuration is resolved in {@link #openProbe}, on a servlet thread, where a
 * failure is reportable.
 */
public final class DiagAgent {

  /**
   * Probe API generation. The WAR checks this before it redefines anything.
   *
   * <p>The agent jar is on the system class path and cannot be reloaded without restarting the JVM,
   * so a pod that attached an older agent and then received a newer WAR would otherwise have a
   * class rewritten to call a method that is not there — and the {@code NoSuchMethodError} would
   * surface inside application's own call path, which is a far worse failure than anything the WAR
   * can produce on its own. Bumping this is how that is caught before the redefinition rather than
   * after.
   *
   * <p>v3 added the descriptor parameter to {@link #probeEnter}. The signature changed rather than
   * gaining an overload, because the emitter names exactly one and two would only make it possible
   * to inject the wrong one.
   *
   * <p>v4 added {@link #releaseProbe} and {@link #probeKeysOwnedBy}, which the WAR needs to restore
   * a class held by more than one span. Nothing injected changed, so a v3 agent would run the
   * rewritten code perfectly well — it would fail at the restore instead, which is the worse
   * moment: the class is already patched and the call that puts it back is the one missing.
   *
   * <p>v5 added {@link #probeArmingsOwnedBy} and the armings a ring now keeps, which the WAR needs
   * to arm a SECOND span into a class a first span already armed. A v4 agent has no record of the
   * first arming, so a WAR that assumed it did would rebuild the class from the new span alone and
   * disarm the old one — reporting a clean capture of nothing. That was the live failure: the
   * preservation plan arms ActivityDal three ways in one step and only the last-armed of the three
   * ever recorded an event.
   *
   * <p>v6 added the event budget: a probe records {@code events} of them (default 100) and stops,
   * reporting {@code complete} so the span disarms. Nothing injected changed and a v5 agent would
   * run the rewritten code perfectly well — it would ignore the budget instead, which is worse than
   * it sounds. An unknown option is ignored by design, so a caller who asked for fifty events would
   * be given an unbounded capture and told nothing, and an instrumented method on a hot path would
   * keep paying the probe cost for the life of its lease.
   *
   * <p>v7 changed what an event carries and one injected signature. Arguments and return values are
   * object-graph snapshots rather than {@code String.valueOf} text, so the JSON a reader decodes
   * changed shape; and {@link #probeEnter} gained the call site's line, so the viewer can show the
   * code that made each call. The signature changed rather than gaining an overload, as in v3, and
   * a v6 agent would fail the WAR's signature check before anything was rewritten.
   *
   * <p>v8 added {@code alloc}: a probe opened with it carries {@code allocBytes} on the events that
   * close a frame. Nothing injected changed, so the WAR asks for v8 only when a span asks for
   * {@code alloc} — a v7 agent would ignore the unknown option and report a capture with no
   * allocation and no reason.
   *
   * <p>v9 added {@code gc}: {@code gcCount} and {@code gcPauseMs} on the events that close a frame,
   * and the span's figure on every read. Gated the same way: only a span asking for {@code gc}
   * needs v9.
   *
   * <p>v10 added root-call filters: {@code minCost}, {@code exceptionsOnly} and {@code match}. A
   * filtered probe holds each root call on its thread and publishes it only when it qualifies,
   * counting the rest as {@code filtered} on every read. Nothing injected changed; gated like
   * {@code alloc} and {@code gc}, because a v9 agent would ignore the options and hand back every
   * call as though each had matched.
   */
  public static final int PROBE_API_VERSION = 10;

  /** Set by {@link #agentmain}; read reflectively from the webapp classloader. */
  public static volatile Instrumentation INSTRUMENTATION;

  /**
   * Pre-patch class bytes, keyed {@code <loaderHash>|<binary name>}.
   *
   * <p>This lives here, on the system class path, rather than in the WAR: a redeployed WAR gets a
   * fresh classloader and would lose the map, while the classes it patched stay patched — leaving a
   * JVM running modified code with no way back. Both the key and the value are JDK types, so the
   * WAR can read and write this map reflectively across the classloader boundary.
   */
  public static final Map<String, byte[]> ORIGINAL_BYTES = new ConcurrentHashMap<String, byte[]>();

  /**
   * Patch leases, keyed {@code <loaderHash>|<binary name>} as {@link #ORIGINAL_BYTES} is, valued
   * with a {@link System#currentTimeMillis} deadline. A patch whose deadline passes is restored by
   * the reaper.
   */
  public static final Map<String, Long> LEASES = new ConcurrentHashMap<String, Long>();

  /** Restores one leased patch. Installed by the WAR, which owns redefinition. */
  public static volatile Restorer RESTORER;

  /** Open probe buffers, keyed by probe key. */
  private static final ConcurrentHashMap<String, ProbeRing> PROBES =
      new ConcurrentHashMap<String, ProbeRing>();

  /**
   * The end state of recently closed probes, so a reader that polls once more after a span ends is
   * told the span finished rather than getting a 404 that reads like a mistyped key. Bounded, and
   * evicted oldest-first.
   */
  private static final ConcurrentHashMap<String, Map<String, Object>> CLOSED =
      new ConcurrentHashMap<String, Map<String, Object>>();

  private static final List<String> CLOSED_ORDER = new ArrayList<String>();
  private static final int CLOSED_RETAINED = 32;

  private static final AtomicInteger OPEN_KEYS = new AtomicInteger();

  /**
   * Bumped whenever a probe closes, so a thread holding frames for a probe that has gone drops them
   * the next time it records rather than carrying the depth forever. See {@link
   * ProbeThreadState#resetIfStale}.
   */
  private static final AtomicLong PROBE_EPOCH = new AtomicLong();

  /**
   * Per-thread depth, frame ids and open-invocation start times. Not an inheritable ThreadLocal: a
   * child thread starts its own call stack, and inheriting the parent's depth would report nesting
   * that never happened.
   */
  private static final ThreadLocal<ProbeThreadState> STATE =
      new ThreadLocal<ProbeThreadState>() {
        @Override
        protected ProbeThreadState initialValue() {
          return new ProbeThreadState(Thread.currentThread());
        }
      };

  /** Set by the WAR so it can tell whether the reaper is already running. */
  private static volatile Thread reaper;

  /** How a lapsed lease is undone, without the agent jar depending on the WAR. */
  public interface Restorer {
    /**
     * @param key {@code <loaderHash>|<binary name>}
     */
    void restore(String key) throws Exception;
  }

  private DiagAgent() {}

  // ---------------------------------------------------------------- probes

  /**
   * @return {@link #PROBE_API_VERSION}, so the WAR can refuse a stale agent.
   */
  public static int probeApiVersion() {
    return PROBE_API_VERSION;
  }

  /**
   * Opens a buffer for {@code key}, owned by {@code owner} (the {@code <loaderHash>|<binary name>}
   * of the class being instrumented, so a restore or a lapsed lease can close it without a second
   * registry).
   *
   * <p>Called before the class is rewritten. A refusal here — the key cap is full — therefore
   * reaches the operator while the JVM is still untouched, rather than after a redefinition that
   * would record into nothing.
   */
  public static Map<String, Object> openProbe(
      String key, String owner, Map<String, Object> options) {
    ProbeConfig config = ProbeConfig.resolve(options);
    Map<String, Object> result = new LinkedHashMap<String, Object>();
    result.put("key", key);
    if (!config.enabled) {
      result.put("opened", Boolean.FALSE);
      result.put("error", "probes are disabled on this JVM (telltale.diag.probe.enabled=false)");
      return result;
    }
    String unmeasurable = unmeasurable(config);
    if (unmeasurable != null) {
      result.put("opened", Boolean.FALSE);
      result.put("error", unmeasurable);
      return result;
    }
    ProbeRing existing = PROBES.get(key);
    if (existing != null) {
      // A span instrumenting several methods arms them one at a time under
      // ONE key, so a second open on a live key can be that span adding
      // another class. It has to SAY so: two spans that collided on a key
      // would otherwise merge their events into one ring silently, and a
      // capture that quietly contains someone else's calls is worse than
      // one that refused to start.
      //
      // Joining adds an owner, and the ring then closes only when the LAST
      // of them is restored. Options are not re-read — the first open
      // sized the ring and the caps, and resizing under a live writer is
      // not something this can do.
      if (!config.join) {
        result.put("opened", Boolean.FALSE);
        result.put(
            "error",
            "probe key "
                + key
                + " is already open; pass join:true to record"
                + " another method of the same span into it");
        return result;
      }
      String mismatch = measurementMismatch(key, existing.config, config);
      if (mismatch != null) {
        result.put("opened", Boolean.FALSE);
        result.put("error", mismatch);
        return result;
      }
      if (owner != null) {
        existing.owners.add(owner);
        existing.arm(owner, options);
      }
      result.put("opened", Boolean.TRUE);
      result.put("joined", Boolean.TRUE);
      result.put("generation", Long.valueOf(existing.generation));
      result.put("capacity", Integer.valueOf(existing.capacity));
      putMeasurements(result, config);
      return result;
    }
    if (OPEN_KEYS.get() >= config.maxKeys) {
      result.put("opened", Boolean.FALSE);
      result.put(
          "error",
          "this JVM already has "
              + OPEN_KEYS.get()
              + " probes open ("
              + describeOpenKeys()
              + ") and the cap is "
              + config.maxKeys
              + "; close one or raise telltale.diag.probe.keys");
      return result;
    }
    ProbeRing ring = new ProbeRing(key, owner, config);
    if (PROBES.putIfAbsent(key, ring) != null) {
      result.put("opened", Boolean.FALSE);
      result.put("error", "probe key " + key + " is already open");
      return result;
    }
    OPEN_KEYS.incrementAndGet();
    CLOSED.remove(key);
    ring.arm(owner, options);
    result.put("opened", Boolean.TRUE);
    result.put("generation", Long.valueOf(ring.generation));
    result.put("capacity", Integer.valueOf(ring.capacity));
    putMeasurements(result, config);
    return result;
  }

  /** Why an opt-in measurement this open asked for cannot be taken, or null. */
  private static String unmeasurable(ProbeConfig config) {
    if (config.allocError != null) {
      return "alloc was requested but cannot be measured: " + config.allocError;
    }
    if (config.gcError != null) {
      return "gc was requested but cannot be measured: " + config.gcError;
    }
    if (config.filterError != null) {
      return "the call filter cannot be evaluated: " + config.filterError;
    }
    return null;
  }

  /**
   * Refuses a join that disagrees with the ring about what it measures. The ring measures
   * allocation and GC or it does not; a join that disagreed would get the ring's answer and believe
   * its own.
   */
  private static String measurementMismatch(String key, ProbeConfig open, ProbeConfig join) {
    if (open.alloc != join.alloc) {
      return "probe key "
          + key
          + " is open with alloc="
          + open.alloc
          + " and this join asked for alloc="
          + join.alloc
          + "; every target of a span is armed with the same alloc";
    }
    if (open.gc != join.gc) {
      return "probe key "
          + key
          + " is open with gc="
          + open.gc
          + " and this join asked for gc="
          + join.gc
          + "; every target of a span is armed with the same gc";
    }
    String opened = open.filter == null ? "none" : open.filter.describe();
    String joining = join.filter == null ? "none" : join.filter.describe();
    if (!opened.equals(joining)) {
      return "probe key "
          + key
          + " is open with filter "
          + opened
          + " and this join asked for "
          + joining
          + "; every target of a span is armed with the same filter";
    }
    return null;
  }

  /** Confirms in an open's answer each opt-in measurement the ring takes. */
  private static void putMeasurements(Map<String, Object> result, ProbeConfig config) {
    if (config.alloc) {
      result.put("alloc", Boolean.TRUE);
    }
    if (config.gc) {
      result.put("gc", Boolean.TRUE);
    }
    if (config.filter != null) {
      result.put("filter", config.filter.describe());
    }
  }

  /** What the filter withheld: calls decided against, and events cut from kept calls. */
  private static void putFiltered(Map<String, Object> result, ProbeRing ring) {
    result.put("filtered", Long.valueOf(ring.filteredCount()));
    result.put("omitted", Long.valueOf(ring.omittedCount()));
  }

  /**
   * The collections the JVM ran since a gc probe opened, as {@code gc: {count, pauseMs}}. This is
   * the span's figure: frames nest and overlap across threads, so summing their per-frame deltas
   * would count one pause many times. Left out when a counter cannot be read, rather than reported
   * as zero.
   */
  private static void putSpanGc(Map<String, Object> result, ProbeRing ring) {
    Map<String, Object> span =
        ring.config.gcCounters == null ? null : ring.config.gcCounters.sinceOpen();
    if (span != null) {
      result.put("gc", span);
    }
  }

  /**
   * Fixes the span's GC figure when recording stops, so later reads report the span, not the JVM
   * since.
   */
  private static void stopGc(ProbeRing ring) {
    if (ring.config.gcCounters != null) {
      ring.config.gcCounters.stop();
    }
  }

  /** Closes one probe, releasing its buffer. Safe on a key that is not open. */
  public static Map<String, Object> closeProbe(String key) {
    ProbeRing ring = PROBES.remove(key);
    if (ring == null) {
      Map<String, Object> absent = new LinkedHashMap<String, Object>();
      absent.put("key", key);
      absent.put("closed", Boolean.FALSE);
      return absent;
    }
    OPEN_KEYS.decrementAndGet();
    if (ring.freeze()) {
      // Any frame this probe had open will never receive its exit, so every
      // thread's depth has to forget them rather than stay raised.
      PROBE_EPOCH.incrementAndGet();
    }
    stopGc(ring);
    Map<String, Object> end = new LinkedHashMap<String, Object>();
    end.put("key", key);
    end.put("closed", Boolean.TRUE);
    end.put("generation", Long.valueOf(ring.generation));
    end.put("writeSeq", Long.valueOf(ring.writeSeq()));
    end.put("capacity", Integer.valueOf(ring.capacity));
    putSpanGc(end, ring);
    putFiltered(end, ring);
    ring.close();
    rememberClosed(key, end);
    return end;
  }

  /** Stops one probe accepting events while retaining its readable buffer. */
  public static Map<String, Object> freezeProbe(String key) {
    ProbeRing ring = key == null ? null : PROBES.get(key);
    if (ring == null) {
      throw new IllegalArgumentException("no probe is open for key " + key);
    }
    boolean changed = ring.freeze();
    if (changed) {
      PROBE_EPOCH.incrementAndGet();
    }
    stopGc(ring);
    Map<String, Object> result = new LinkedHashMap<String, Object>();
    result.put("key", key);
    result.put("frozen", Boolean.TRUE);
    result.put("changed", Boolean.valueOf(changed));
    result.put("generation", Long.valueOf(ring.generation));
    result.put("writeSeq", Long.valueOf(ring.writeSeq()));
    result.put("capacity", Integer.valueOf(ring.capacity));
    return result;
  }

  /**
   * Releases {@code owner}'s claim on every probe recording for it, closing the ones no longer
   * claimed by anything.
   *
   * <p>This is what a restore and the lease reaper call. Without it a killed client leaks its
   * buffer for the life of the JVM: probe keys carry a random per-span suffix, so every abandoned
   * span would strand a distinct one that nothing would ever look up again.
   *
   * <p>It releases rather than closes because a span may instrument several classes into one ring,
   * and they are restored one at a time. Closing on the first would leave the rest instrumented and
   * recording into a closed ring — probeEnter early-returns, no counter moves, and the span reports
   * a method that never ran. So the ring survives until its last owner is gone.
   *
   * <p>Returns only the probes that actually closed, so a caller can report what it ended rather
   * than what it merely let go of.
   */
  public static List<Map<String, Object>> closeProbesOwnedBy(String owner) {
    List<Map<String, Object>> closed = new ArrayList<Map<String, Object>>();
    if (owner == null) {
      return closed;
    }
    for (Iterator<Map.Entry<String, ProbeRing>> it = PROBES.entrySet().iterator(); it.hasNext(); ) {
      Map.Entry<String, ProbeRing> entry = it.next();
      ProbeRing ring = entry.getValue();
      if (!ring.owners.remove(owner)) {
        continue;
      }
      if (ring.owners.isEmpty()) {
        closed.add(closeProbe(entry.getKey()));
      }
    }
    return closed;
  }

  /**
   * Releases one probe's claim on {@code owner}, closing its ring if that was the last class it
   * recorded for.
   *
   * <p>The single-key counterpart of {@link #closeProbesOwnedBy}, and what a restore calls when it
   * knows which span is finishing. The difference matters when several spans instrument ONE class:
   * releasing by owner alone would take every one of their rings down together, so the first span
   * to finish would silently disarm the rest.
   *
   * @return {@code released} — whether this key was open and did record for that class — and {@code
   *     closed}, whether that was its last owner. A caller decides from {@link #probeKeysOwnedBy},
   *     not from these, whether the class's original bytes may go back.
   */
  public static Map<String, Object> releaseProbe(String key, String owner) {
    ProbeRing ring = key == null ? null : PROBES.get(key);
    if (ring == null || owner == null || !ring.owners.remove(owner)) {
      Map<String, Object> nothing = new LinkedHashMap<String, Object>();
      nothing.put("key", key);
      nothing.put("released", Boolean.FALSE);
      nothing.put("closed", Boolean.FALSE);
      return nothing;
    }
    if (!ring.owners.isEmpty()) {
      Map<String, Object> held = new LinkedHashMap<String, Object>();
      held.put("key", key);
      held.put("released", Boolean.TRUE);
      held.put("closed", Boolean.FALSE);
      held.put("owners", new ArrayList<String>(ring.owners));
      return held;
    }
    Map<String, Object> end = new LinkedHashMap<String, Object>(closeProbe(key));
    end.put("released", Boolean.TRUE);
    return end;
  }

  /**
   * The keys of every open probe still recording for {@code owner}.
   *
   * <p>The reverse of the {@code owners} a ring carries, and the question a restore has to answer
   * before it puts a class's original bytes back: those bytes are shared by every span instrumented
   * into the class, so restoring while another span still holds it disarms that span without
   * telling it.
   */
  public static List<String> probeKeysOwnedBy(String owner) {
    List<String> keys = new ArrayList<String>();
    if (owner == null) {
      return keys;
    }
    for (Map.Entry<String, ProbeRing> entry : PROBES.entrySet()) {
      if (entry.getValue().owners.contains(owner)) {
        keys.add(entry.getKey());
      }
    }
    return keys;
  }

  /**
   * Everything armed into {@code owner}'s class that is still recording, in the order it was armed.
   *
   * <p>The question the WAR has to answer BEFORE it rewrites a class, and the counterpart of {@link
   * #probeKeysOwnedBy}, which answers it before a restore. A class is redefined whole, so arming a
   * second method replaces the first method's rewritten bytes along with everything else — the
   * second span has to rebuild the union, and this is where it reads what the union is.
   *
   * <p>Each row is the {@code options} the WAR opened the probe with, plus the {@code owner} it
   * armed and the {@code key} it records under. The agent puts nothing of its own in and reads
   * nothing out: what the rows mean is the WAR's business, which is what keeps a change to the
   * rewriting from needing an agent that can only be replaced by restarting the JVM.
   */
  public static List<Map<String, Object>> probeArmingsOwnedBy(String owner) {
    List<Map<String, Object>> armings = new ArrayList<Map<String, Object>>();
    if (owner == null) {
      return armings;
    }
    for (Map.Entry<String, ProbeRing> entry : PROBES.entrySet()) {
      for (Map<String, Object> armed : entry.getValue().armings) {
        if (!owner.equals(armed.get("owner"))) {
          continue;
        }
        Map<String, Object> row = new LinkedHashMap<String, Object>(armed);
        row.put("key", entry.getKey());
        armings.add(row);
      }
    }
    Collections.sort(
        armings,
        new Comparator<Map<String, Object>>() {
          @Override
          public int compare(Map<String, Object> left, Map<String, Object> right) {
            return Long.valueOf(armedSeq(left)).compareTo(Long.valueOf(armedSeq(right)));
          }
        });
    return armings;
  }

  private static long armedSeq(Map<String, Object> arming) {
    Object seq = arming.get("armedSeq");
    return seq instanceof Number ? ((Number) seq).longValue() : 0L;
  }

  /**
   * Closes every open probe.
   *
   * <p>The operator's blunt instrument for "stop recording, whatever is going on" — a probe keeps
   * its buffer until something closes it, and this closes all of them without needing to know their
   * keys. It does not restore any class: an instrumented method whose probe is closed simply
   * records nothing, which is why this is safe to call at any time.
   */
  public static List<Map<String, Object>> closeAllProbes() {
    List<Map<String, Object>> closed = new ArrayList<Map<String, Object>>();
    for (String key : new ArrayList<String>(PROBES.keySet())) {
      closed.add(closeProbe(key));
    }
    return closed;
  }

  /** Every open probe, for diagnosing what a JVM is still recording. */
  public static List<Map<String, Object>> probeKeys() {
    List<Map<String, Object>> keys = new ArrayList<Map<String, Object>>();
    for (Map.Entry<String, ProbeRing> entry : PROBES.entrySet()) {
      ProbeRing ring = entry.getValue();
      Map<String, Object> row = new LinkedHashMap<String, Object>();
      row.put("key", entry.getKey());
      // A list, not the first owner under the old singular name: a reader
      // deciding whether a restore will interrupt a capture needs every
      // class the ring records for, and one of several is a wrong answer
      // that looks like a right one. This route is a live read with nothing
      // persisted behind it, so the rename is clean.
      row.put("owners", new ArrayList<String>(ring.owners));
      row.put("generation", Long.valueOf(ring.generation));
      row.put("capacity", Integer.valueOf(ring.capacity));
      row.put("writeSeq", Long.valueOf(ring.writeSeq()));
      row.put("oldestSeq", Long.valueOf(ring.oldestSeq()));
      row.put("degraded", Boolean.valueOf(ring.isDegraded()));
      keys.add(row);
    }
    return keys;
  }

  /**
   * Reads forward from {@code sinceSeq} without removing anything.
   *
   * <p>Every value crossing back is a JDK type, because the caller is the WAR reaching in through
   * the system classloader. The events themselves are already-rendered JSON lines: the WAR forwards
   * them without understanding them and the CLI appends them to its archive verbatim.
   *
   * <p>Non-destructive is deliberate. It lets a following CLI and a browser watch one span at once,
   * and it removes the race a destructive drain has, where an event written between the read and
   * the removal is lost with nothing to show it ever existed.
   */
  public static Map<String, Object> drain(String key, long sinceSeq, int limit) {
    Map<String, Object> result = new LinkedHashMap<String, Object>();
    result.put("key", key);
    ProbeRing ring = PROBES.get(key);
    if (ring == null) {
      Map<String, Object> end = CLOSED.get(key);
      if (end != null) {
        result.putAll(end);
        result.put("active", Boolean.FALSE);
        result.put("events", new ArrayList<String>());
        return result;
      }
      result.put("active", Boolean.FALSE);
      result.put("closed", Boolean.FALSE);
      result.put("error", "no probe is open for key " + key);
      return result;
    }
    ProbeRing.Batch batch = ring.read(sinceSeq, limit <= 0 ? 1000 : limit);
    List<String> lines = new ArrayList<String>(batch.events.size());
    for (ProbeEvent event : batch.events) {
      lines.add(ProbeJson.line(event));
    }
    result.put("generation", Long.valueOf(ring.generation));
    result.put("events", lines);
    result.put("nextSeq", Long.valueOf(batch.nextSeq));
    result.put("oldestSeq", Long.valueOf(batch.oldestSeq));
    result.put("writeSeq", Long.valueOf(batch.writeSeq));
    result.put("dropped", Long.valueOf(batch.dropped));
    result.put("truncated", Boolean.valueOf(batch.truncated));
    result.put("suppressed", Long.valueOf(ring.suppressedCount()));
    putFiltered(result, ring);
    result.put("degraded", Boolean.valueOf(ring.isDegraded()));
    result.put("capacity", Integer.valueOf(ring.capacity));
    putSpanGc(result, ring);
    result.put("active", Boolean.valueOf(ring.isActive()));
    result.put("closed", Boolean.FALSE);
    // Reported alongside active rather than folded into it: a complete probe
    // has stopped recording but still holds everything it recorded, which is
    // the opposite of what a reader should conclude from active:false. It is
    // also the signal to disarm — the client that opened the span is the only
    // party that can restore the class without closing buffers nobody has
    // read yet, so this is how it is told there is nothing left to wait for.
    result.put("complete", Boolean.valueOf(ring.isComplete()));
    result.put("maxEvents", Integer.valueOf(ring.config.maxEvents));
    return result;
  }

  // ------------------------------------------------- injected probe points

  /**
   * Opens a frame for one invocation and records that it started.
   *
   * <p>Returns the frame id the matching {@link #probeExit}, {@link #probeReturn} and {@link
   * #probeThrow} carry back, or 0 when nothing is recording — the injected code passes whatever
   * comes back along without inspecting it, so an unopened probe costs one map lookup and nothing
   * else.
   *
   * @param descriptor the raw JVM descriptor of the method entered, pushed as a constant by the
   *     emitter. It is what tells two overloads apart once one probe key covers a set of methods,
   *     which {@code method} on its own cannot.
   * @param line for a call site, the caller's source line the call was made from, pushed as a
   *     constant; 0 for a method's own entry and for a class compiled without line numbers.
   */
  public static long probeEnter(
      String key, String className, String method, String descriptor, int line, Object[] args) {
    try {
      ProbeRing ring = PROBES.get(key);
      if (ring == null || !ring.beginWrite()) {
        return 0L;
      }
      try {
        ProbeThreadState state = state();
        ProbeFilter filter = ring.config.filter;
        ProbePending held = filter == null ? null : state.pending(ring);
        int depth = state.depth();
        // A filtered probe's root renders its arguments even when the ring is
        // degraded: the predicate needs them, and only a kept call is retained.
        // A call already rejected renders nothing more.
        boolean root = filter != null && (held == null || depth <= held.rootDepth);
        boolean rejected = held != null && !root && held.rejected;
        ValueGraph.Node[] rendered =
            renderArgs(ring, state, rejected ? null : args, root && filter.hasArgPredicates());
        long frame = state.nextFrame();
        long parent = state.parentFrame(ring.config.maxDepth);
        // After the arguments are rendered, so the snapshot is not charged
        // to the method it describes.
        long startAlloc = allocated(ring);
        ProbeGc gc = ring.config.gcCounters;
        long startGcCount = gc == null ? -1L : gc.collections();
        long startGcMillis = gc == null ? -1L : gc.collectionMillis();
        long startNanos = System.nanoTime();
        state.push(
            frame, startNanos, startAlloc, startGcCount, startGcMillis, ring.config.maxDepth);
        ProbeEvent event =
            ProbeEvent.enter(
                ring.now(),
                state.threadId,
                state.threadName,
                key,
                depth,
                frame,
                parent,
                className,
                method,
                descriptor,
                line,
                rendered,
                state.renderedBytes,
                state.truncated);
        if (filter == null) {
          ring.add(event);
        } else if (!root) {
          held.hold(event);
        } else {
          holdRoot(ring, state, held, event, filter.matchesArgs(rendered));
        }
        return frame;
      } finally {
        ring.endWrite();
      }
    } catch (Throwable ignored) {
      // See the class javadoc: observation must never break the observed.
      return 0L;
    }
  }

  /**
   * Records a returned value and hands it back unchanged, so the instrumented method's own {@code
   * return} keeps its meaning.
   *
   * <p>The identity of the value matters more than it looks: a probe that normalised, interned or
   * copied what it observed would be a probe that changed the program it was measuring.
   */
  public static <T> T probeReturn(String key, long frame, T value) {
    try {
      ProbeRing ring = PROBES.get(key);
      if (ring != null && frame != 0L && ring.beginWrite()) {
        try {
          ProbeThreadState state = state();
          if (ring.config.allocation != null) {
            // Before the value is snapshotted: the exit that follows
            // closes the frame on this reading, not on one that
            // includes rendering what the method returned.
            state.markReturn(frame, allocated(ring));
          }
          ProbeFilter filter = ring.config.filter;
          ProbePending held = filter == null ? null : state.pending(ring);
          boolean root = held != null && held.rootFrame == frame;
          if (filter == null || (held != null && !held.rejected)) {
            ValueGraph.Node rendered =
                renderValue(ring, state, value, root && filter.hasReturnPredicates());
            ProbeEvent event =
                ProbeEvent.returned(
                    ring.now(),
                    state.threadId,
                    state.threadName,
                    key,
                    state.depth(),
                    frame,
                    rendered,
                    state.renderedBytes,
                    state.truncated);
            if (filter == null) {
              ring.add(event);
            } else if (root) {
              held.returnMatched = filter.matchesReturn(rendered);
              held.close(event);
            } else {
              held.hold(event);
            }
          }
        } finally {
          ring.endWrite();
        }
      }
    } catch (Throwable ignored) {
      // Fall through: the value is returned either way.
    }
    return value;
  }

  /** Records an exception on its way out, closing the frame it escaped. */
  public static void probeThrow(String key, long frame, Throwable error) {
    try {
      ProbeRing ring = PROBES.get(key);
      if (ring == null || frame == 0L || !ring.beginWrite()) {
        return;
      }
      try {
        ProbeThreadState state = state();
        long endAlloc = allocated(ring);
        ProbeGc gc = ring.config.gcCounters;
        long endGcCount = gc == null ? -1L : gc.collections();
        long endGcMillis = gc == null ? -1L : gc.collectionMillis();
        long startNanos = state.popTo(frame);
        long allocBytes = ProbeAllocation.delta(state.poppedAlloc(), endAlloc);
        String rendered = describeThrowable(ring, state, error);
        publishClose(
            ring,
            state,
            ProbeEvent.thrown(
                ring.now(),
                state.threadId,
                state.threadName,
                key,
                state.depth(),
                frame,
                rendered,
                elapsed(startNanos),
                allocBytes,
                ProbeAllocation.delta(state.poppedGcCount(), endGcCount),
                ProbeAllocation.delta(state.poppedGcMillis(), endGcMillis),
                state.lastUnwound(),
                state.truncated),
            true);
      } finally {
        ring.endWrite();
      }
    } catch (Throwable ignored) {
      // As above.
    }
  }

  /** Closes a frame that finished normally, with how long it took. */
  public static void probeExit(String key, long frame) {
    try {
      ProbeRing ring = PROBES.get(key);
      if (ring == null || frame == 0L || !ring.beginWrite()) {
        return;
      }
      try {
        ProbeThreadState state = state();
        long endAlloc = state.takeReturnAlloc(frame);
        if (endAlloc < 0L) {
          endAlloc = allocated(ring);
        }
        ProbeGc gc = ring.config.gcCounters;
        long endGcCount = gc == null ? -1L : gc.collections();
        long endGcMillis = gc == null ? -1L : gc.collectionMillis();
        long startNanos = state.popTo(frame);
        publishClose(
            ring,
            state,
            ProbeEvent.exited(
                ring.now(),
                state.threadId,
                state.threadName,
                key,
                state.depth(),
                frame,
                elapsed(startNanos),
                ProbeAllocation.delta(state.poppedAlloc(), endAlloc),
                ProbeAllocation.delta(state.poppedGcCount(), endGcCount),
                ProbeAllocation.delta(state.poppedGcMillis(), endGcMillis),
                state.lastUnwound()),
            false);
      } finally {
        ring.endWrite();
      }
    } catch (Throwable ignored) {
      // As above.
    }
  }

  // ---------------------------------------------------------- call filters

  /**
   * Starts holding a filtered probe's root call on this thread. A call the probe was still holding
   * here is one whose close was lost (an exception escaping uninstrumented code, a probe opened
   * mid-call): it can never be decided, so it is counted as filtered and let go.
   */
  private static void holdRoot(
      ProbeRing ring,
      ProbeThreadState state,
      ProbePending abandoned,
      ProbeEvent enter,
      boolean argsMatched) {
    if (abandoned != null) {
      state.release(abandoned);
      ring.filter();
    }
    ProbePending call = new ProbePending(ring, enter.frame, enter.depth, !argsMatched);
    state.hold(call);
    call.hold(enter);
  }

  /**
   * Publishes an event that closes a frame. For a filtered probe it is held with its call, and the
   * root's own close decides the whole call: published in order, or counted as {@code filtered}.
   */
  private static void publishClose(
      ProbeRing ring, ProbeThreadState state, ProbeEvent event, boolean threw) {
    ProbeFilter filter = ring.config.filter;
    if (filter == null) {
      ring.add(event);
      return;
    }
    ProbePending held = state.pending(ring);
    if (held == null) {
      // A frame entered before this probe opened: there is no call to decide.
      return;
    }
    if (event.frame != held.rootFrame) {
      if (event.depth > held.rootDepth) {
        held.hold(event);
        return;
      }
      // Unwound past the root: its own close was lost and never comes.
      state.release(held);
      ring.filter();
      return;
    }
    held.close(event);
    state.release(held);
    if (!held.rejected && filter.keeps(event.durationNs, threw, held.returnMatched)) {
      held.publish();
    } else {
      ring.filter();
    }
  }

  // ------------------------------------------------------ value rendering

  /** This thread's state, with frames from a closed probe already forgotten. */
  private static ProbeThreadState state() {
    ProbeThreadState state = STATE.get();
    state.resetIfStale(PROBE_EPOCH.get());
    return state;
  }

  private static long elapsed(long startNanos) {
    return startNanos < 0L ? 0L : System.nanoTime() - startNanos;
  }

  /** The thread's allocation counter when the ring measures it, else -1. */
  private static long allocated(ProbeRing ring) {
    ProbeAllocation allocation = ring.config.allocation;
    return allocation == null ? -1L : allocation.currentThreadBytes();
  }

  /**
   * Snapshots the arguments, all of them through ONE walk: they share the event's node and text
   * budgets, and an object passed twice is shown once.
   */
  private static ValueGraph.Node[] renderArgs(
      ProbeRing ring, ProbeThreadState state, Object[] args, boolean evenIfDegraded) {
    state.truncated = false;
    state.renderedBytes = 0L;
    if (args == null
        || ProbeConfig.ARGS_NONE.equals(ring.config.argMode)
        || (ring.isDegraded() && !evenIfDegraded)) {
      return null;
    }
    int count = Math.min(args.length, ring.config.maxArgs);
    ValueGraph.Node[] rendered = new ValueGraph.Node[count + (count < args.length ? 1 : 0)];
    ValueGraph graph = new ValueGraph(ring.config.graph);
    for (int i = 0; i < count; i++) {
      rendered[i] = describe(ring, state, graph, args[i], "arg" + i);
    }
    if (count < args.length) {
      rendered[count] = ValueGraph.marker("(truncated)", (args.length - count) + " more args");
      state.truncated = true;
    }
    state.truncated |= graph.truncated();
    state.renderedBytes = graph.bytes();
    return rendered;
  }

  private static ValueGraph.Node renderValue(
      ProbeRing ring, ProbeThreadState state, Object value, boolean evenIfDegraded) {
    state.truncated = false;
    state.renderedBytes = 0L;
    if (ProbeConfig.ARGS_NONE.equals(ring.config.argMode)
        || (ring.isDegraded() && !evenIfDegraded)) {
      return null;
    }
    ValueGraph graph = new ValueGraph(ring.config.graph);
    ValueGraph.Node rendered = describe(ring, state, graph, value, "return");
    state.truncated = graph.truncated();
    state.renderedBytes = graph.bytes();
    return rendered;
  }

  /**
   * Snapshots one value for reporting.
   *
   * <p>The snapshot is an object graph built by reading fields, never by calling the value's own
   * methods: {@code toString()} on a lazily initialised persistence proxy can fire a query or dirty
   * a session, and one that throws or reaches another probe would make the diagnostic the failure.
   * application's DCLs do not override {@code toString()} anyway, so text would only have said
   * {@code ActivityDcl@d7c405bf}. The walk is bounded in depth, children, nodes and characters,
   * because what it builds sits in the ring until it is read — an application {@code XMLData}
   * payload is the obvious value that would otherwise fill the heap of the JVM being diagnosed.
   *
   * <p>{@link ProbeConfig#ARGS_TYPE} still exists for the cheapest capture: the class and identity
   * only, reading nothing at all.
   */
  private static ValueGraph.Node describe(
      ProbeRing ring, ProbeThreadState state, ValueGraph graph, Object value, String label) {
    if (value != null && ProbeConfig.ARGS_TYPE.equals(ring.config.argMode)) {
      return ValueGraph.opaque(label, value);
    }
    if (state.inProbe) {
      ring.suppress();
      return ValueGraph.marker(label, "(not rendered: already inside a probe)");
    }
    state.inProbe = true;
    try {
      return graph.root(value, label, null);
    } catch (Throwable t) {
      // The walk catches what a field read can throw; this is for anything
      // else — an OutOfMemoryError building a node — so the method still runs.
      return ValueGraph.marker(label, "(not rendered: " + t.getClass().getName() + ")");
    } finally {
      state.inProbe = false;
    }
  }

  /**
   * The exception as its class and message, read without calling a subclass's getMessage(): that is
   * application's code, on application's thread.
   */
  private static String describeThrowable(ProbeRing ring, ProbeThreadState state, Throwable error) {
    state.truncated = false;
    if (error == null) {
      return null;
    }
    return truncate(state, InertTypes.throwableText(error), ring.config.argChars);
  }

  /**
   * Cuts at the cap and says how much it cut, so a truncation is never silent. Marks {@code state}
   * when it cuts, so the event can say it was truncated without the reader having to spot the
   * marker in the text.
   */
  private static String truncate(ProbeThreadState state, String value, int max) {
    if (value == null || value.length() <= max) {
      return value;
    }
    state.truncated = true;
    return value.substring(0, max) + "…(+" + (value.length() - max) + " chars)";
  }

  private static String describeOpenKeys() {
    StringBuilder open = new StringBuilder();
    for (String key : PROBES.keySet()) {
      if (open.length() > 0) {
        open.append(", ");
      }
      open.append(key);
    }
    return open.toString();
  }

  private static synchronized void rememberClosed(String key, Map<String, Object> end) {
    CLOSED.put(key, end);
    CLOSED_ORDER.add(key);
    while (CLOSED_ORDER.size() > CLOSED_RETAINED) {
      CLOSED.remove(CLOSED_ORDER.remove(0));
    }
  }

  // ---------------------------------------------------------------- leases

  /** Extends a patch's lease. */
  public static void lease(String key, long millis) {
    LEASES.put(key, System.currentTimeMillis() + millis);
  }

  /**
   * Starts the daemon that restores patches whose lease has lapsed, once.
   *
   * <p>This is what makes a killed client safe. The span that installed a patch renews its lease
   * while it runs; if the process holding it dies — a killed CLI, a dropped exec channel, a Ctrl-C
   * — nothing renews, and the JVM puts the original bytes back on its own instead of serving
   * patched code indefinitely.
   */
  public static synchronized void startReaper() {
    if (reaper != null && reaper.isAlive()) {
      return;
    }
    Thread thread =
        new Thread(
            new Runnable() {
              @Override
              public void run() {
                while (true) {
                  try {
                    Thread.sleep(5000L);
                    reapExpired();
                  } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    return;
                  } catch (Throwable ignored) {
                    // Keep reaping: one class that cannot be restored must
                    // not strand every other lease behind it.
                  }
                }
              }
            },
            "telltale-diag-lease-reaper");
    thread.setDaemon(true);
    thread.start();
    reaper = thread;
  }

  /** Restores every patch whose deadline has passed. Visible for testing. */
  public static void reapExpired() {
    Restorer restorer = RESTORER;
    if (restorer == null) {
      return;
    }
    long now = System.currentTimeMillis();
    for (Map.Entry<String, Long> entry :
        new ArrayList<Map.Entry<String, Long>>(LEASES.entrySet())) {
      if (entry.getValue() == null || entry.getValue() > now) {
        continue;
      }
      // Drop the lease first: a restore that fails must not be retried
      // every five seconds for the life of the JVM.
      LEASES.remove(entry.getKey());
      // Free the buffers before the restore, so a restore that fails still
      // releases the memory. The class is coming back either way, and a
      // probe on un-instrumented code would never record again regardless.
      closeProbesOwnedBy(entry.getKey());
      try {
        restorer.restore(entry.getKey());
      } catch (Throwable ignored) {
        // The class may have gone with its classloader (a webapp
        // redeploy), which makes the stale lease harmless.
      }
    }
  }

  public static void agentmain(String args, Instrumentation instrumentation) {
    INSTRUMENTATION = instrumentation;
  }

  public static void premain(String args, Instrumentation instrumentation) {
    INSTRUMENTATION = instrumentation;
  }
}
