package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.CyclicBarrier;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;

/**
 * The ring under many writers at once, which is the only condition its design exists for.
 *
 * <p>The load-bearing assertion is a conservation law: every event a writer produced was either
 * read or counted as dropped, exactly once, with nothing double-counted and nothing lost silently.
 * It is checkable at all only because loss is <em>derived</em> from the sequence rather than
 * tallied by a counter — a counter would race the writers it counts, and the arithmetic here would
 * then be the thing that failed rather than the thing that proved the design.
 */
class ProbeRingConcurrencyTest {

  private static final int WRITERS = 8;
  private static final int PER_WRITER = 50_000;
  private static final int TOTAL = WRITERS * PER_WRITER;

  @Test
  void losesNothingWhenTheRingIsLargerThanEverythingWritten() throws Exception {
    Outcome outcome = race(ProbeRing.roundUpToPowerOfTwo(TOTAL * 2));

    assertTrue(outcome.failures.isEmpty(), () -> "writers threw: " + outcome.failures);
    assertEquals(0L, outcome.dropped, "a ring larger than the run cannot evict anything");
    assertEquals(TOTAL, outcome.observed.size(), "every event should have been read");
    assertEquals(TOTAL, outcome.distinctSequences, "sequences must be unique");
  }

  @Test
  void accountsForEveryEventItEvictedWhenTheRingIsSmallerThanTheRun() throws Exception {
    Outcome outcome = race(1024);

    assertTrue(outcome.failures.isEmpty(), () -> "writers threw: " + outcome.failures);
    // The claim of the whole design: read + dropped == produced, exactly.
    assertEquals(
        TOTAL,
        outcome.observed.size() + outcome.dropped,
        "every produced event must be either observed or accounted as dropped");
    assertEquals(
        outcome.observed.size(), outcome.distinctSequences, "no sequence may be observed twice");
    assertTrue(
        outcome.dropped > 0L,
        "a 1024-slot ring taking " + TOTAL + " events must have evicted some");
  }

  /**
   * Runs the writers against one ring while a reader cursors continuously, then drains whatever is
   * left once the writers are done.
   */
  private Outcome race(int capacity) throws Exception {
    ProbeRing ring = new ProbeRing("Fixture.run.test", "owner", config(capacity));
    CyclicBarrier start = new CyclicBarrier(WRITERS);
    CountDownLatch finished = new CountDownLatch(WRITERS);
    List<Throwable> failures = new CopyOnWriteArrayList<Throwable>();
    AtomicBoolean writing = new AtomicBoolean(true);

    Outcome outcome = new Outcome(failures);
    Thread reader = new Thread(new Reader(ring, outcome, writing), "probe-reader");
    reader.setDaemon(true);

    List<Thread> writers = new ArrayList<Thread>();
    for (int w = 0; w < WRITERS; w++) {
      Thread thread = new Thread(new Writer(ring, start, finished, failures), "probe-writer-" + w);
      thread.setDaemon(true);
      writers.add(thread);
    }

    reader.start();
    for (Thread thread : writers) {
      thread.start();
    }
    finished.await();
    for (Thread thread : writers) {
      thread.join();
    }
    writing.set(false);
    reader.join();

    // Whatever the reader had not reached by the time the writers stopped.
    outcome.consume(ring.read(outcome.cursor, Integer.MAX_VALUE));
    // Release the ring's charge against the shared budget. A ring dropped
    // without closing keeps its bytes charged for the life of the JVM, which
    // would push every later probe in this run into degraded mode.
    ring.close();
    return outcome;
  }

  private static ProbeConfig config(int capacity) {
    java.util.Map<String, Object> options = new java.util.HashMap<String, Object>();
    options.put("ring", Integer.valueOf(capacity));
    // A budget far above anything this test writes, so degradation never
    // fires and the counts under test are the ring's alone.
    options.put("budget-bytes", Integer.valueOf(Integer.MAX_VALUE));
    return ProbeConfig.resolve(options);
  }

  /** Accumulates what the reader saw, and what it was told it missed. */
  private static final class Outcome {
    private final Set<Long> observed = new HashSet<Long>();
    private final List<Throwable> failures;
    private long dropped;
    private long cursor;
    private int distinctSequences;

    Outcome(List<Throwable> failures) {
      this.failures = failures;
    }

    synchronized void consume(ProbeRing.Batch batch) {
      dropped += batch.dropped;
      for (ProbeEvent event : batch.events) {
        observed.add(Long.valueOf(event.seq));
      }
      cursor = batch.nextSeq;
      distinctSequences = observed.size();
    }
  }

  private static final class Reader implements Runnable {
    private final ProbeRing ring;
    private final Outcome outcome;
    private final AtomicBoolean writing;

    Reader(ProbeRing ring, Outcome outcome, AtomicBoolean writing) {
      this.ring = ring;
      this.outcome = outcome;
      this.writing = writing;
    }

    @Override
    public void run() {
      while (writing.get()) {
        outcome.consume(ring.read(outcome.cursor, 4096));
      }
    }
  }

  private static final class Writer implements Runnable {
    private final ProbeRing ring;
    private final CyclicBarrier start;
    private final CountDownLatch finished;
    private final List<Throwable> failures;

    Writer(ProbeRing ring, CyclicBarrier start, CountDownLatch finished, List<Throwable> failures) {
      this.ring = ring;
      this.start = start;
      this.finished = finished;
      this.failures = failures;
    }

    @Override
    public void run() {
      try {
        start.await();
        for (int i = 0; i < PER_WRITER; i++) {
          ring.add(
              ProbeEvent.enter(
                  ring.now(),
                  Thread.currentThread().getId(),
                  "t",
                  "Fixture.run.test",
                  0,
                  i,
                  0L,
                  "Fixture",
                  "run",
                  "()V",
                  0,
                  null,
                  0L,
                  false));
        }
      } catch (Throwable t) {
        failures.add(t);
      } finally {
        finished.countDown();
      }
    }
  }

  @Test
  void stopsAtAClaimedButUnpublishedTicketRatherThanSkippingIt() {
    ProbeRing ring = new ProbeRing("Fixture.run.gap", "owner", config(8));
    ring.add(event(ring));
    ring.add(event(ring));

    // Simulate a writer that has taken sequence 2 but not yet published: the
    // reader must stop and report 2 as its cursor, not step over it to 3.
    // Skipping would silently drop an event that is about to appear.
    ProbeEvent third = event(ring);
    ring.add(third);
    third.seq = 99L;

    ProbeRing.Batch batch = ring.read(0L, Integer.MAX_VALUE);
    assertEquals(2, batch.events.size(), "the two published events are readable");
    assertEquals(2L, batch.nextSeq, "the cursor stops at the unpublished ticket");
    assertEquals(0L, batch.dropped, "an unpublished ticket is not a loss");
  }

  @Test
  void reportsWhatScrolledPastRatherThanResumingSilently() {
    ProbeRing ring = new ProbeRing("Fixture.run.lap", "owner", config(8));
    for (int i = 0; i < 15; i++) {
      ring.add(event(ring));
    }

    ProbeRing.Batch batch = ring.read(0L, Integer.MAX_VALUE);
    assertEquals(7L, batch.dropped, "15 events through 8 slots leaves the first 7 gone");
    assertEquals(8, batch.events.size(), "the most recent 8 survive — the oldest are dropped");
    assertEquals(7L, batch.events.get(0).seq, "the window starts after what was evicted");
    assertEquals(15L, batch.nextSeq);
  }

  @Test
  void releasesItsChargeAndStopsRecordingWhenClosed() {
    ProbeRing ring = new ProbeRing("Fixture.run.close", "owner", config(8));
    ring.add(event(ring));
    ring.close();

    assertTrue(!ring.isActive(), "a closed ring records nothing further");
    assertNull(
        ring.read(0L, Integer.MAX_VALUE).events.stream().findFirst().orElse(null),
        "closing clears what it held");
  }

  private static ProbeEvent event(ProbeRing ring) {
    return ProbeEvent.enter(
        ring.now(), 1L, "t", ring.key, 0, 1L, 0L, "Fixture", "run", "()V", 0, null, 0L, false);
  }
}
