package com.flanksource.telltale.diag.agent;

/**
 * What one thread is in the middle of, so an event can say how deep it is and which invocation it
 * belongs to.
 *
 * <p>Confined to its owning thread, so nothing here is synchronized and nothing needs to be. That
 * is also why frame ids can be a plain field increment rather than an atomic: a parent invocation
 * is always on the same thread as its child, so ids only have to be unique per thread and the pair
 * {@code (threadId, frame)} is what identifies an invocation downstream.
 */
final class ProbeThreadState {

  final long threadId;

  /**
   * Cached, because {@code Thread.getName()} allocates on the Java 8 target — it returns {@code new
   * String(char[], true)} on every call — and this would otherwise be an allocation per probe
   * inside application code. A thread renamed after first touch keeps its old name here, which is
   * why events also carry {@link #threadId}.
   */
  final String threadName;

  /** Slots per open invocation in {@link #stack}. */
  private static final int STRIDE = 5;

  /**
   * Interleaved {frame, startNanos, startAlloc, startGcCount, startGcMillis}, one group per open
   * invocation. startAlloc is -1 unless the probe measures allocation, and the two GC counters -1
   * unless it measures GC.
   */
  private long[] stack = new long[32 * STRIDE];

  private int depth;
  private long nextFrame;
  private int lastUnwound;

  /** The startAlloc of the frame the most recent {@link #popTo} closed, or -1. */
  private long poppedAlloc = -1L;

  /** The GC counters the frame the most recent {@link #popTo} closed opened at, or -1. */
  private long poppedGcCount = -1L;

  private long poppedGcMillis = -1L;

  /**
   * The allocation counter as {@code probeReturn} read it for {@link #returnFrame}, BEFORE it
   * snapshotted the value — so the exit that follows charges the method, not the rendering of what
   * it returned. -1 when there is none.
   */
  private long returnAlloc = -1L;

  private long returnFrame;

  /** The probe generation these open frames belong to; see {@link #resetIfStale}. */
  private long epoch = -1L;

  /**
   * Forgets any open frames when the set of open probes has changed since this thread last
   * recorded.
   *
   * <p>Closing a probe strands whatever frames it had open: the matching exit arrives, finds the
   * buffer gone, and returns without popping — so the depth would stay raised for the life of the
   * thread. On a pooled request thread that is permanent, and every later trace on it would report
   * nesting that never happened. Frames belonging to a probe that is gone can never be closed, so
   * dropping them is the repair rather than a loss.
   */
  void resetIfStale(long currentEpoch) {
    if (epoch == currentEpoch) {
      return;
    }
    epoch = currentEpoch;
    depth = 0;
    lastUnwound = 0;
    returnAlloc = -1L;
    // A held call's frames are gone with the rest; it can never end.
    pending.clear();
  }

  /**
   * The root calls filtered probes hold on this thread, one per probe at most and usually none. A
   * list rather than one slot: two filtered spans may nest on one thread.
   */
  private final java.util.ArrayList<ProbePending> pending = new java.util.ArrayList<ProbePending>(2);

  /** The call {@code ring} is holding on this thread, or null. */
  ProbePending pending(ProbeRing ring) {
    for (int i = 0; i < pending.size(); i++) {
      if (pending.get(i).ring == ring) {
        return pending.get(i);
      }
    }
    return null;
  }

  void hold(ProbePending call) {
    pending.add(call);
  }

  void release(ProbePending call) {
    pending.remove(call);
  }

  /** Set when the last rendered value hit a length cap. */
  boolean truncated;

  /** What the last value snapshot retains, for the event's charge against the budget. */
  long renderedBytes;

  /**
   * Set while snapshotting a value, so a rendering that reaches an instrumented method declines
   * instead of recursing. The snapshot runs no application code, so what is left to guard is the
   * JDK's own {@code toString()} on a value type — reachable only by instrumenting a JDK class —
   * and unbounded recursion inside application code is the price of not guarding it.
   */
  boolean inProbe;

  ProbeThreadState(Thread thread) {
    this.threadId = thread.getId();
    this.threadName = thread.getName();
  }

  int depth() {
    return depth;
  }

  long nextFrame() {
    return ++nextFrame;
  }

  /** The enclosing invocation's frame, or 0 when there is none or it went past the cap. */
  long parentFrame(int maxDepth) {
    int top = depth - 1;
    if (top < 0 || top >= maxDepth) {
      return 0L;
    }
    return stack[top * STRIDE];
  }

  /**
   * Opens a frame. Beyond {@code maxDepth} the depth still counts but the frame is not recorded —
   * the cap bounds memory, not correctness, and a run that deep is pathological recursion rather
   * than something anyone is reading.
   */
  void push(
      long frame,
      long startNanos,
      long startAlloc,
      long startGcCount,
      long startGcMillis,
      int maxDepth) {
    if (depth < maxDepth) {
      ensure(depth);
      int base = depth * STRIDE;
      stack[base] = frame;
      stack[base + 1] = startNanos;
      stack[base + 2] = startAlloc;
      stack[base + 3] = startGcCount;
      stack[base + 4] = startGcMillis;
    }
    depth++;
  }

  /** Holds the allocation counter {@code probeReturn} read for {@code frame}. */
  void markReturn(long frame, long allocated) {
    returnFrame = frame;
    returnAlloc = allocated;
  }

  /**
   * The counter {@link #markReturn} held for {@code frame}, consumed, or -1 when it holds none for
   * that frame and the caller has to read the counter itself.
   */
  long takeReturnAlloc(long frame) {
    long allocated = returnFrame == frame ? returnAlloc : -1L;
    returnAlloc = -1L;
    return allocated;
  }

  /**
   * Closes {@code frame} and everything opened inside it, returning the nanos it started at, or -1
   * when it is not on the stack.
   *
   * <p>Unwinding TO a frame rather than popping one is what keeps depth honest. If an inner exit
   * was lost — an exception escaping a method instrumented without a handler, or a probe closed
   * mid-flight — popping one would leave the depth permanently high on a pooled Tomcat thread, and
   * every later trace on that thread would read as nested when it was not. Unwinding to the named
   * frame self-heals on the next outer exit, and {@link #lastUnwound()} reports the repair so it is
   * visible rather than silent.
   */
  long popTo(long frame) {
    int recorded = Math.min(depth, stack.length / STRIDE);
    for (int position = recorded - 1; position >= 0; position--) {
      if (stack[position * STRIDE] == frame) {
        lastUnwound = depth - 1 - position;
        long startNanos = stack[position * STRIDE + 1];
        poppedAlloc = stack[position * STRIDE + 2];
        poppedGcCount = stack[position * STRIDE + 3];
        poppedGcMillis = stack[position * STRIDE + 4];
        depth = position;
        return startNanos;
      }
    }
    // Not recorded: either past the depth cap or from a frame opened before
    // this probe existed. Give back one level rather than none, so depth
    // still trends back to zero instead of climbing forever.
    lastUnwound = 0;
    poppedAlloc = -1L;
    poppedGcCount = -1L;
    poppedGcMillis = -1L;
    if (depth > 0) {
      depth--;
    }
    return -1L;
  }

  /** Frames closed beyond the one named, from the most recent {@link #popTo}. */
  int lastUnwound() {
    return lastUnwound;
  }

  /** The allocation counter the frame closed by the most recent {@link #popTo} opened at, or -1. */
  long poppedAlloc() {
    return poppedAlloc;
  }

  /** The GC collection count the most recently closed frame opened at, or -1. */
  long poppedGcCount() {
    return poppedGcCount;
  }

  /** The GC collection milliseconds the most recently closed frame opened at, or -1. */
  long poppedGcMillis() {
    return poppedGcMillis;
  }

  private void ensure(int index) {
    int required = (index + 1) * STRIDE;
    if (required <= stack.length) {
      return;
    }
    long[] grown = new long[stack.length * 2];
    System.arraycopy(stack, 0, grown, 0, stack.length);
    stack = grown;
  }
}
