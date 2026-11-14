package com.flanksource.telltale.diag.agent;

import com.flanksource.telltale.diag.graph.ValueGraph;

/**
 * One thing an instrumented method did: it was entered, it returned a value, it threw, or it
 * finished.
 *
 * <p>Immutable on purpose: events are published across threads through {@link ProbeRing} with no
 * lock, and the only thing that makes that safe is that nothing mutates after publication. The
 * value snapshots are trees, but the walk finishes them before the event is built and nothing
 * touches them afterwards.
 *
 * <p>This class deliberately holds <em>no static state</em>. Its first use is inside an
 * instrumented method, so its {@code <clinit>} runs on an application thread — where a failure
 * would be an {@code ExceptionInInitializerError} in application's own call path, permanent for the
 * life of the JVM. Rendering lives in {@link ProbeJson}, which is only ever touched by a drain on a
 * servlet thread.
 */
final class ProbeEvent {

  static final String ENTER = "enter";
  static final String RETURN = "return";
  static final String THROW = "throw";
  static final String EXIT = "exit";

  /**
   * Position in this ring's sequence.
   *
   * <p>The one non-final field, written exactly once by {@link ProbeRing#add} immediately before
   * the {@code lazySet} that publishes the reference. That ordering is what makes it safe without a
   * volatile write: {@code lazySet} carries a StoreStore barrier, so this store lands before the
   * reference does, and the reader's {@code AtomicReferenceArray.get()} is a volatile read. It is
   * never written again, and never written by a reader.
   */
  long seq;

  /** Epoch nanos, anchored per ring — see {@link ProbeRing#now()}. */
  final long ts;

  final long threadId;
  final String thread;

  /** One of {@link #ENTER}, {@link #RETURN}, {@link #THROW}, {@link #EXIT}. */
  final String kind;

  final String key;

  /** Call depth on this thread, 0 for an outermost instrumented call. */
  final int depth;

  /** Identifies the invocation; unique per thread, so the key is (threadId, frame). */
  final long frame;

  /** The enclosing invocation's frame, or 0 when there is none or it is unknown. */
  final long parent;

  /** Set on {@link #ENTER} only; the other kinds are tied to it by {@code frame}. */
  final String className;

  final String method;

  /**
   * The raw JVM descriptor of the method entered, e.g. {@code (I)Ljava/lang/String;}.
   *
   * <p>Carried per event rather than looked up from whatever armed the probe, because one probe key
   * may cover a SET of methods: {@code method} alone cannot say which overload ran, and anything
   * keyed on the name alone — a rollup, a call tree — would merge overloads whose costs are the
   * reason they were traced separately.
   *
   * <p>Raw, not reduced to the simple names a person writes. This is what goes into the {@code
   * .jsonl}, which should hold what the JVM said; formatting is a read-side choice and it is not
   * reversible once made.
   */
  final String descriptor;

  /**
   * For a call site, the line in the CALLER's source the call was made from; 0 for a method's own
   * entry, whose caller is not in view, and for a class compiled without line numbers. Set on
   * {@link #ENTER} only.
   */
  final int line;

  /** Snapshotted arguments, or null when the caller did not ask for them. */
  final ValueGraph.Node[] args;

  /** Snapshotted return value, {@link #RETURN} only. */
  final ValueGraph.Node value;

  /**
   * What the snapshots above retain, as the walk estimated it. Carried rather than recomputed,
   * because {@link #charge()} runs on every add and again on every eviction.
   */
  final long valueBytes;

  /** Rendered throwable, {@link #THROW} only. */
  final String error;

  /** Elapsed time of the invocation, on the events that close a frame. */
  final long durationNs;

  /**
   * Bytes the thread allocated between the frame's entry and its exit, on the events that close a
   * frame of a probe opened with {@code alloc}; -1 when the probe did not ask, or the frame's entry
   * reading is unknown (past the depth cap, or the counter could not be read). Never a 0 standing
   * in for "unknown".
   */
  final long allocBytes;

  /**
   * Collections that ran JVM-wide while the frame was open, and their accumulated time in
   * milliseconds, on the events that close a frame of a probe opened with {@code gc}; -1 when the
   * probe did not ask or a counter is unknown.
   */
  final long gcCount;

  final long gcPauseMs;

  /**
   * How many frames this event unwound beyond its own — non-zero only when an inner exit was lost
   * and the depth self-healed. Reported rather than hidden, because a silent repair reads as a
   * depth that was never wrong.
   */
  final int unwound;

  /** True when any rendered text in this event hit a length cap. */
  final boolean truncated;

  private ProbeEvent(
      long ts,
      long threadId,
      String thread,
      String kind,
      String key,
      int depth,
      long frame,
      long parent,
      String className,
      String method,
      String descriptor,
      int line,
      ValueGraph.Node[] args,
      ValueGraph.Node value,
      long valueBytes,
      String error,
      long durationNs,
      long allocBytes,
      long gcCount,
      long gcPauseMs,
      int unwound,
      boolean truncated) {
    this.ts = ts;
    this.threadId = threadId;
    this.thread = thread;
    this.kind = kind;
    this.key = key;
    this.depth = depth;
    this.frame = frame;
    this.parent = parent;
    this.className = className;
    this.method = method;
    this.descriptor = descriptor;
    this.line = line;
    this.args = args;
    this.value = value;
    this.valueBytes = valueBytes;
    this.error = error;
    this.durationNs = durationNs;
    this.allocBytes = allocBytes;
    this.gcCount = gcCount;
    this.gcPauseMs = gcPauseMs;
    this.unwound = unwound;
    this.truncated = truncated;
  }

  static ProbeEvent enter(
      long ts,
      long threadId,
      String thread,
      String key,
      int depth,
      long frame,
      long parent,
      String className,
      String method,
      String descriptor,
      int line,
      ValueGraph.Node[] args,
      long valueBytes,
      boolean truncated) {
    return new ProbeEvent(
        ts,
        threadId,
        thread,
        ENTER,
        key,
        depth,
        frame,
        parent,
        className,
        method,
        descriptor,
        line,
        args,
        null,
        valueBytes,
        null,
        0L,
        -1L,
        -1L,
        -1L,
        0,
        truncated);
  }

  static ProbeEvent returned(
      long ts,
      long threadId,
      String thread,
      String key,
      int depth,
      long frame,
      ValueGraph.Node value,
      long valueBytes,
      boolean truncated) {
    return new ProbeEvent(
        ts,
        threadId,
        thread,
        RETURN,
        key,
        depth,
        frame,
        0L,
        null,
        null,
        null,
        0,
        null,
        value,
        valueBytes,
        null,
        0L,
        -1L,
        -1L,
        -1L,
        0,
        truncated);
  }

  static ProbeEvent thrown(
      long ts,
      long threadId,
      String thread,
      String key,
      int depth,
      long frame,
      String error,
      long durationNs,
      long allocBytes,
      long gcCount,
      long gcPauseMs,
      int unwound,
      boolean truncated) {
    return new ProbeEvent(
        ts,
        threadId,
        thread,
        THROW,
        key,
        depth,
        frame,
        0L,
        null,
        null,
        null,
        0,
        null,
        null,
        0L,
        error,
        durationNs,
        allocBytes,
        gcCount,
        gcPauseMs,
        unwound,
        truncated);
  }

  static ProbeEvent exited(
      long ts,
      long threadId,
      String thread,
      String key,
      int depth,
      long frame,
      long durationNs,
      long allocBytes,
      long gcCount,
      long gcPauseMs,
      int unwound) {
    return new ProbeEvent(
        ts,
        threadId,
        thread,
        EXIT,
        key,
        depth,
        frame,
        0L,
        null,
        null,
        null,
        0,
        null,
        null,
        0L,
        null,
        durationNs,
        allocBytes,
        gcCount,
        gcPauseMs,
        unwound,
        false);
  }

  /**
   * Rough retained size, used to charge this event against the global budget.
   *
   * <p>An estimate rather than a measurement: the point is a bound that moves with what is actually
   * held, not an exact heap figure. Strings are charged at two bytes per char because they are
   * UTF-16 on the Java 8 target, and the value snapshots at what their walk estimated.
   */
  long charge() {
    long bytes = 128L + valueBytes;
    bytes += chars(thread) + chars(key) + chars(className) + chars(method) + chars(descriptor);
    bytes += chars(error);
    if (args != null) {
      bytes += 16L * args.length;
    }
    return bytes;
  }

  private static int chars(String s) {
    return s == null ? 0 : 2 * s.length();
  }
}
