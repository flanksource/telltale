package com.flanksource.telltale.diag.agent;

import com.flanksource.telltale.diag.graph.GraphLimits;
import java.util.Map;

/**
 * The bounds one probe records under, resolved once when the probe is opened.
 *
 * <p>Resolved <em>once</em>, and on the servlet thread that opened the probe, for two reasons.
 * {@code System.getProperty} takes the {@code Properties} monitor and allocates, which has no
 * business on a path an instrumented method runs; and a {@code SecurityManager} can deny it, which
 * is reportable on a request thread and an {@code ExceptionInInitializerError} in application's
 * call path anywhere else. Everything the hot path needs is a final field here.
 *
 * <p>Every bound takes a JVM-wide default from a system property and can be overridden per probe. A
 * probe on a method taking a large DTO wants tighter caps than the JVM default, and a restart-only
 * knob would be no use at all.
 */
final class ProbeConfig {

  /**
   * Values are snapshotted as object graphs — the default. Fields are read, not getters called, and
   * only JDK value types are turned into text, so the snapshot runs none of the application's code;
   * see {@code ValueGraph}.
   */
  static final String ARGS_VALUE = "value";

  /** Only the type and identity hash, reading nothing from the value. */
  static final String ARGS_TYPE = "type";

  /** Arguments and return values are not recorded at all. */
  static final String ARGS_NONE = "none";

  private static final String PREFIX = "telltale.diag.probe.";

  /** Pre-v2 name for the ring capacity, still honoured. */
  private static final String LEGACY_LIMIT = "telltale.diag.probe-limit";

  final int ringCapacity;

  /**
   * Events one probe records before it stops recording, arthas' {@code -n}.
   *
   * <p>A capture is a sample, not a tap. Left running, an instrumented method on a hot path pays
   * the probe cost on every call for the life of its lease and produces a ring holding the last few
   * seconds of a run nobody is reading — the useful part having scrolled past long before. Stopping
   * at a budget is what makes "instrument this and see" a safe thing to do on a busy JVM.
   *
   * <p>Distinct from {@link #ringCapacity}, which is how much is KEPT. This is how much is ever
   * WRITTEN, and reaching it ends the recording rather than evicting anything.
   */
  final int maxEvents;

  final int maxKeys;
  final int maxArgs;
  final int maxDepth;
  final String argMode;
  final boolean enabled;
  final long budgetBytes;

  /**
   * How far a value snapshot may go: {@code expand} levels, {@code children} per container, and —
   * across one whole event, all of its arguments together — {@code nodes} and {@code event-chars}
   * characters, each string capped at {@code arg-chars} from its head and tail.
   *
   * <p>Generous by default, because a capture that cannot be read is a capture that was paid for
   * and wasted: 512 characters cut the preservation run's query builder off before its {@code NOT
   * IN}, which was the one part that changed between calls.
   */
  final GraphLimits graph;

  /** Per-string cap, also applied to an exception's text. */
  final int argChars;

  /**
   * The caller means to record into a key that is already open.
   *
   * <p>Per-open intent, never a system property: a JVM-wide default would turn off the one check
   * that tells "this span is arming its second method" apart from "two spans landed on one key",
   * and the second silently merges someone else's calls into a capture. A span arming a set of
   * methods sets it; every other caller leaves it false and a repeat open is refused.
   */
  final boolean join;

  /**
   * The caller asked for {@code alloc}: each frame's exit carries the bytes its thread allocated
   * since the entry. Per-open intent, never a system property — it is a call into the JVM on every
   * entry and exit of the observed method.
   */
  final boolean alloc;

  /** The counter {@link #alloc} reads, or null when it was not asked for or is unusable. */
  final ProbeAllocation allocation;

  /** Why {@link #alloc} was asked for and cannot be honoured; the open is refused with it. */
  final String allocError;

  /**
   * The caller asked for {@code gc}: each frame's exit carries the collections that ran JVM-wide
   * while it was open, and every read the span's total.
   */
  final boolean gc;

  /** The counters {@link #gc} reads, or null when it was not asked for or is unusable. */
  final ProbeGc gcCounters;

  /** Why {@link #gc} was asked for and cannot be honoured; the open is refused with it. */
  final String gcError;

  private ProbeConfig(
      int ringCapacity,
      int maxEvents,
      int maxKeys,
      int maxArgs,
      int maxDepth,
      String argMode,
      boolean enabled,
      long budgetBytes,
      boolean join,
      GraphLimits graph,
      int argChars,
      boolean alloc,
      boolean gc) {
    this.ringCapacity = ringCapacity;
    this.maxEvents = maxEvents;
    this.maxKeys = maxKeys;
    this.maxArgs = maxArgs;
    this.maxDepth = maxDepth;
    this.argMode = argMode;
    this.enabled = enabled;
    this.budgetBytes = budgetBytes;
    this.join = join;
    this.graph = graph;
    this.argChars = argChars;
    this.alloc = alloc;
    ProbeAllocation resolved = null;
    String error = null;
    if (alloc) {
      try {
        resolved = ProbeAllocation.resolve();
      } catch (RuntimeException | LinkageError unusable) {
        error = reason(unusable);
      }
    }
    this.allocation = resolved;
    this.allocError = error;
    this.gc = gc;
    ProbeGc counters = null;
    String gcFailure = null;
    if (gc) {
      try {
        counters = ProbeGc.resolve();
      } catch (RuntimeException | LinkageError unusable) {
        gcFailure = reason(unusable);
      }
    }
    this.gcCounters = counters;
    this.gcError = gcFailure;
  }

  private static String reason(Throwable unusable) {
    return unusable.getMessage() != null ? unusable.getMessage() : unusable.getClass().getName();
  }

  /**
   * Resolves the JVM defaults, then applies whatever the caller overrode.
   *
   * <p>{@code options} may be null; unknown entries are ignored rather than rejected, so an older
   * CLI talking to a newer agent still opens a probe.
   */
  static ProbeConfig resolve(Map<String, Object> options) {
    int capacity =
        ProbeRing.roundUpToPowerOfTwo(positive(pick(options, "ring", legacyCapacity()), 4096));
    int argChars = positive(pick(options, "arg-chars", prefixed("arg-chars", 16384)), 16384);
    GraphLimits graph =
        GraphLimits.probe(
            positive(pick(options, "expand", prefixed("expand", 3)), 3),
            positive(pick(options, "children", prefixed("children", 100)), 100),
            positive(pick(options, "nodes", prefixed("nodes", 2000)), 2000),
            argChars,
            positive(pick(options, "event-chars", prefixed("event-chars", 262144)), 262144));
    return new ProbeConfig(
        capacity,
        positive(pick(options, "events", prefixed("events", 1000)), 1000),
        positive(pick(options, "keys", prefixed("keys", 8)), 8),
        positive(pick(options, "max-args", prefixed("max-args", 16)), 16),
        positive(pick(options, "depth", prefixed("depth", 256)), 256),
        argMode(options),
        boolOption(options, "enabled", boolProperty("enabled", true)),
        positive(
            pick(options, "budget-bytes", prefixed("budget-bytes", 256 * 1024 * 1024)),
            256 * 1024 * 1024),
        boolOption(options, "join", false),
        graph,
        argChars,
        boolOption(options, "alloc", false),
        boolOption(options, "gc", false));
  }

  /**
   * The ring capacity's JVM default, honouring the pre-v2 property name.
   *
   * <p>{@code telltale.diag.probe-limit} bounded a list that dropped the newest frame; it now sizes
   * a ring that drops the oldest. The number means the same thing — how much is kept — so an
   * operator who set it keeps getting what they asked for rather than silently reverting to the
   * default.
   */
  private static int legacyCapacity() {
    int current = intProperty(PREFIX + "ring", -1);
    if (current > 0) {
      return current;
    }
    return intProperty(LEGACY_LIMIT, 4096);
  }

  private static String argMode(Map<String, Object> options) {
    Object raw = options == null ? null : options.get("arg-mode");
    String mode = raw == null ? System.getProperty(PREFIX + "arg-mode") : String.valueOf(raw);
    if (ARGS_TYPE.equalsIgnoreCase(mode)) {
      return ARGS_TYPE;
    }
    if (ARGS_NONE.equalsIgnoreCase(mode)) {
      return ARGS_NONE;
    }
    // Anything unrecognised means "render values", the documented default. A
    // typo must not silently disable capture — that reads as a quiet method.
    return ARGS_VALUE;
  }

  private static int pick(Map<String, Object> options, String name, int fallback) {
    Object raw = options == null ? null : options.get(name);
    if (raw instanceof Number) {
      return ((Number) raw).intValue();
    }
    if (raw != null) {
      try {
        return Integer.parseInt(String.valueOf(raw).trim());
      } catch (RuntimeException ignored) {
        return fallback;
      }
    }
    return fallback;
  }

  private static boolean boolOption(Map<String, Object> options, String name, boolean fallback) {
    Object raw = options == null ? null : options.get(name);
    if (raw instanceof Boolean) {
      return ((Boolean) raw).booleanValue();
    }
    if (raw != null) {
      return Boolean.parseBoolean(String.valueOf(raw).trim());
    }
    return fallback;
  }

  /** Reads {@code telltale.diag.probe.<name>}. */
  private static int prefixed(String name, int fallback) {
    return intProperty(PREFIX + name, fallback);
  }

  private static int intProperty(String fullName, int fallback) {
    try {
      String configured = System.getProperty(fullName);
      if (configured != null) {
        return Integer.parseInt(configured.trim());
      }
    } catch (RuntimeException ignored) {
      // A malformed override falls back to the default rather than
      // disabling the bound, which is the failure that costs a heap.
    }
    return fallback;
  }

  private static boolean boolProperty(String name, boolean fallback) {
    try {
      String configured = System.getProperty(PREFIX + name);
      if (configured != null) {
        return Boolean.parseBoolean(configured.trim());
      }
    } catch (RuntimeException ignored) {
      // As above: an unreadable property must not turn the probe off.
    }
    return fallback;
  }

  private static int positive(int value, int fallback) {
    return value > 0 ? value : fallback;
  }
}
