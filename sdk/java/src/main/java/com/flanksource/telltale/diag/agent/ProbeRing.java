package com.flanksource.telltale.diag.agent;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReferenceArray;

/**
 * A bounded, sequenced, drop-oldest buffer of one probe's events.
 *
 * <p>Writers are instrumented application methods, so the governing rule is that a write must never
 * block, never allocate beyond the event itself, and never throw. The whole write is a {@code
 * getAndIncrement} for the ticket and a {@code lazySet} to publish:
 *
 * <pre>
 *     long s = seq.getAndIncrement();
 *     event.seq = s;
 *     slots.lazySet((int) (s &amp; mask), event);
 * </pre>
 *
 * <p>Eviction is implicit — overwriting the slot <em>is</em> the eviction — which is why this drops
 * the OLDEST rather than the newest. A span that outruns its reader keeps the most recent window
 * instead of freezing on the first N events, and the reader is told exactly how much scrolled past.
 *
 * <p>Loss is derived, never counted. With {@code writeSeq} events written into {@code capacity}
 * slots, everything below {@code writeSeq - capacity} is gone, so a reader holding cursor {@code c}
 * missed {@code oldestSeq - c}. There is no drop counter to race, which is what makes the
 * conservation law {@code observed + dropped == produced} exact under any interleaving.
 *
 * <p>Reads are wait-free and take no lock, so they never interact with a writer. The per-slot
 * sequence check is what makes that safe: a slot holding some other sequence is either one that has
 * been lapped (count it and move on) or one whose writer holds the ticket but has not published yet
 * (stop there and come back).
 */
final class ProbeRing {

  /**
   * Live bytes this ring is holding, used only to decide when to degrade.
   *
   * <p>Per ring rather than shared across all of them, which is what keeps this free of
   * process-global state: the total is already bounded by the key cap times the per-ring budget, so
   * a global counter would buy nothing and cost the one failure mode it is easy to miss — a ring
   * dropped without being closed would keep its bytes charged against every later probe for the
   * life of the JVM.
   *
   * <p>Approximate by construction: two writers wrapping onto one slot at the same instant can
   * mis-charge each other. That is fine, because it steers a heuristic rather than enforcing a
   * limit anything depends on being exact.
   */
  private final AtomicLong charge = new AtomicLong();

  private final AtomicReferenceArray<ProbeEvent> slots;
  private final AtomicLong seq = new AtomicLong();
  private final int mask;

  final int capacity;
  final String key;

  /**
   * Every class this ring records for, as {@code <loaderHash>|<binary name>} — the same key the
   * byte stash and the leases use.
   *
   * <p>A SET rather than one owner because a span may instrument several classes into one ring.
   * Restoring one of them must not close the ring while the others are still instrumented: they
   * would keep running rewritten bytecode recording into nothing, and the span would go silent
   * without a counter moving. The ring closes when the last owner goes.
   *
   * <p>Concurrent because openProbe and the lease reaper touch it from different threads, and
   * iteration during a close must not throw.
   */
  final Set<String> owners = Collections.newSetFromMap(new ConcurrentHashMap<String, Boolean>());

  /**
   * What the WAR rewrote into each owner's class for this probe, one entry per arm, oldest first.
   *
   * <p>Opaque here: the agent neither reads nor validates it, it only keeps it alive exactly as
   * long as the buffer it records into. That is the point. Instrumenting a class replaces all of
   * its bytes at once, so a second span arming a class a first span already armed must rewrite BOTH
   * — and to do that it has to be able to ask what the first one was. Holding the answer anywhere
   * else lets the two disagree: in the WAR it would be lost on a redeploy, which leaves the classes
   * patched, and the next arm would then silently disarm every span that survived.
   */
  final List<Map<String, Object>> armings = new CopyOnWriteArrayList<Map<String, Object>>();

  final ProbeConfig config;

  /**
   * This ring's share of the JVM-wide budget. Dividing by the key cap is what makes the per-ring
   * bound add up to a global one: however many probes are open, together they cannot hold more than
   * the budget.
   */
  private final long budgetBytes;

  /**
   * Identifies this opening of the key. Probe keys carry a per-span random suffix so reuse is
   * unlikely, but a close/open cycle would reset the sequence to zero and make a stale cursor look
   * like a future one. A reader that passes back a generation it no longer matches is told the
   * buffer reset rather than handed a batch that looks continuous.
   */
  final long generation;

  /** Wall-clock anchor; see {@link #now()}. */
  private final long anchorEpochNanos;

  private final long anchorNanoTime;

  private volatile boolean active = true;
  private volatile boolean complete;
  private volatile boolean degraded;
  private final AtomicInteger writers = new AtomicInteger();
  private final AtomicLong suppressed = new AtomicLong();
  private final AtomicLong filtered = new AtomicLong();
  private final AtomicLong omitted = new AtomicLong();

  ProbeRing(String key, String owner, ProbeConfig config) {
    this.key = key;
    if (owner != null) {
      this.owners.add(owner);
    }
    this.config = config;
    this.capacity = config.ringCapacity;
    this.budgetBytes = Math.max(1L, config.budgetBytes / Math.max(1, config.maxKeys));
    this.mask = this.capacity - 1;
    this.slots = new AtomicReferenceArray<ProbeEvent>(this.capacity);
    this.generation = System.currentTimeMillis();
    this.anchorEpochNanos = this.generation * 1_000_000L;
    this.anchorNanoTime = System.nanoTime();
  }

  /**
   * Records what was rewritten into {@code owner}'s class under this probe.
   *
   * <p>Stamped with a JVM-wide sequence rather than left to the order a map happens to iterate in,
   * so rebuilding a class from its armings produces the same bytes every time. Two spans arming the
   * SAME method nest one inside the other, and which is inner would otherwise vary between runs.
   */
  void arm(String owner, Map<String, Object> options) {
    if (owner == null) {
      return;
    }
    Map<String, Object> arming = new LinkedHashMap<String, Object>();
    if (options != null) {
      arming.putAll(options);
    }
    arming.put("owner", owner);
    arming.put("armedSeq", Long.valueOf(ARMED.incrementAndGet()));
    armings.add(arming);
  }

  /** Orders armings across every ring; see {@link #arm}. */
  private static final AtomicLong ARMED = new AtomicLong();

  /**
   * Epoch nanoseconds for an event.
   *
   * <p>Java 8 has no nanosecond wall clock, and {@code currentTimeMillis() * 1_000_000} is a
   * millisecond wearing a nanosecond costume: two events in one millisecond would tie and the JSONL
   * would be unorderable. So the wall clock is sampled once, at open, and every event is that
   * anchor plus elapsed {@code nanoTime}. The result is monotonic and correctly spaced. It is NOT
   * NTP-accurate and is only comparable within this JVM — which is enough, because being
   * JVM-monotonic is what lets several probes' events merge into one ordered stream.
   */
  long now() {
    return anchorEpochNanos + (System.nanoTime() - anchorNanoTime);
  }

  /** True while the ring accepts writes. A closed ring records nothing. */
  boolean isActive() {
    return active;
  }

  /**
   * True once the global budget was crossed and events stopped carrying rendered argument and
   * return text. Structure and timing survive: losing the values costs a reader detail, losing the
   * call graph costs them the answer, and an OOM costs them the JVM they were diagnosing.
   */
  boolean isDegraded() {
    return degraded;
  }

  void suppress() {
    suppressed.incrementAndGet();
  }

  long suppressedCount() {
    return suppressed.get();
  }

  /** A root call its filter decided against, or that was abandoned unfinished. */
  void filter() {
    filtered.incrementAndGet();
  }

  long filteredCount() {
    return filtered.get();
  }

  /** Events left out of published calls that outgrew their thread's buffer. */
  void omitted(int count) {
    omitted.addAndGet(count);
  }

  long omittedCount() {
    return omitted.get();
  }

  /** This ring's share of the JVM-wide byte budget, which also bounds a held call. */
  long budgetBytes() {
    return budgetBytes;
  }

  long writeSeq() {
    return seq.get();
  }

  long oldestSeq() {
    return Math.max(0L, seq.get() - capacity);
  }

  /** Publishes one event. Never blocks, never throws, never waits on a reader. */
  void add(ProbeEvent event) {
    long s = seq.getAndIncrement();
    int index = (int) (s & mask);
    ProbeEvent evicted = slots.get(index);
    event.seq = s;
    slots.lazySet(index, event);
    charge(event, evicted);
    if (s + 1 >= config.maxEvents) {
      complete = true;
    }
  }

  /**
   * The event budget is spent: this probe has stopped recording.
   *
   * <p>Distinct from {@link #isActive}, and deliberately so. Closed means the buffer is gone and an
   * empty read says nothing about what ran; complete means the capture finished on its own terms
   * and everything it holds is still there to read. Reporting the second as the first would turn a
   * successful sample into a warning about a revoked recording.
   *
   * <p>Set from a writer with a plain volatile store rather than a compare-and- set: two calls
   * crossing the budget at the same instant both write true, and the count they cross at is a
   * budget, not an invariant.
   */
  boolean isComplete() {
    return complete;
  }

  /**
   * True once recording stopped and the buffer is still readable.
   *
   * <p>What the injected probes check. A frame whose entry was recorded and whose exit was not is
   * the ordinary shape of the last invocation before the budget ran out; readers already tolerate
   * an unclosed frame, and {@link #isComplete} is what explains it.
   */
  boolean isRecording() {
    return active && !complete;
  }

  boolean beginWrite() {
    if (!isRecording()) {
      return false;
    }
    writers.incrementAndGet();
    if (!isRecording()) {
      writers.decrementAndGet();
      return false;
    }
    return true;
  }

  void endWrite() {
    writers.decrementAndGet();
  }

  private void charge(ProbeEvent added, ProbeEvent evicted) {
    long delta = added.charge() - (evicted == null ? 0L : evicted.charge());
    boolean over = charge.addAndGet(delta) > budgetBytes;
    if (over != degraded) {
      degraded = over;
    }
  }

  /** Stops new writes and waits for every admitted writer to publish. */
  synchronized boolean freeze() {
    if (!active) {
      return false;
    }
    active = false;
    while (writers.get() > 0) {
      Thread.yield();
    }
    return true;
  }

  /** Stops the ring recording and lets go of what it held. */
  void close() {
    freeze();
    for (int i = 0; i < capacity; i++) {
      slots.lazySet(i, null);
    }
    charge.set(0L);
  }

  /** One cursored read: the events after {@code sinceSeq}, and what was missed. */
  static final class Batch {
    final List<ProbeEvent> events = new ArrayList<ProbeEvent>();
    long nextSeq;
    long oldestSeq;
    long writeSeq;
    long dropped;
    boolean truncated;
  }

  /**
   * Reads forward from {@code sinceSeq} (inclusive) without removing anything.
   *
   * <p>Non-destructive is what lets two readers — a following CLI and a browser — watch one span at
   * once, and it removes by construction the race a destructive drain has, where an event written
   * between the read and the removal disappears.
   */
  Batch read(long sinceSeq, int limit) {
    Batch batch = new Batch();
    long write = seq.get();
    long oldest = Math.max(0L, write - capacity);
    batch.writeSeq = write;
    batch.oldestSeq = oldest;
    batch.dropped = Math.max(0L, oldest - sinceSeq);

    long s = Math.max(sinceSeq, oldest);
    for (; s < write && batch.events.size() < limit; s++) {
      ProbeEvent event = slots.get((int) (s & mask));
      if (event != null && event.seq == s) {
        batch.events.add(event);
        continue;
      }
      // The slot no longer holds this sequence. Either it was lapped while
      // we read — count it, it is genuinely gone — or a writer claimed the
      // ticket and has not published yet, in which case stopping here and
      // returning s as the cursor picks it up on the next read. Guessing
      // the second case is the first would silently skip a live event.
      if (s < Math.max(0L, seq.get() - capacity)) {
        batch.dropped++;
        continue;
      }
      break;
    }
    batch.nextSeq = s;
    batch.truncated = s < write;
    return batch;
  }

  /** Rounds up to a power of two so the index is a mask rather than a modulo. */
  static int roundUpToPowerOfTwo(int requested) {
    if (requested <= 1) {
      return 1;
    }
    return Integer.highestOneBit(requested - 1) << 1;
  }
}
