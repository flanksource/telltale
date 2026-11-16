package com.flanksource.telltale.diag.agent;

import java.lang.management.ManagementFactory;

/**
 * The calling thread's allocation counter, for a probe opened with {@code alloc: true}.
 *
 * <p>It reads HotSpot's per-thread allocated-bytes counter through {@code
 * com.sun.management.ThreadMXBean}: everything the thread allocated, TLAB and outside it, since it
 * started. A frame's measurement is the difference between two readings, so it counts what the
 * thread allocated between entry and exit — callees included, other threads excluded, and never
 * what is still reachable afterwards.
 *
 * <p>The {@code com.sun.management} type is confined to this class on purpose. {@link #resolve}
 * runs on the servlet thread that opens the probe, so a JVM without the extension fails there, as a
 * refusal the operator reads, and never as a linkage error inside an instrumented method.
 */
final class ProbeAllocation {

  private final com.sun.management.ThreadMXBean threads;

  private ProbeAllocation(com.sun.management.ThreadMXBean threads) {
    this.threads = threads;
  }

  /**
   * The counter, checked usable before any probe depends on it.
   *
   * @throws IllegalStateException naming why this JVM cannot measure per-thread allocation. It is
   *     never enabled on the caller's behalf: that switch is JVM-global, and a diagnostic must not
   *     change the JVM it observes.
   */
  static ProbeAllocation resolve() {
    java.lang.management.ThreadMXBean bean = ManagementFactory.getThreadMXBean();
    if (!(bean instanceof com.sun.management.ThreadMXBean)) {
      throw new IllegalStateException(
          "this JVM's ThreadMXBean ("
              + bean.getClass().getName()
              + ") is not a com.sun.management.ThreadMXBean, so it cannot report per-thread"
              + " allocation");
    }
    com.sun.management.ThreadMXBean threads = (com.sun.management.ThreadMXBean) bean;
    if (!threads.isThreadAllocatedMemorySupported()) {
      throw new IllegalStateException(
          "this JVM does not support per-thread allocation measurement");
    }
    if (!threads.isThreadAllocatedMemoryEnabled()) {
      throw new IllegalStateException(
          "per-thread allocation measurement is disabled on this JVM"
              + " (ThreadMXBean.setThreadAllocatedMemoryEnabled(false))");
    }
    ProbeAllocation allocation = new ProbeAllocation(threads);
    if (allocation.currentThreadBytes() < 0L) {
      throw new IllegalStateException(
          "this JVM reported no allocation counter for the current thread");
    }
    return allocation;
  }

  /**
   * Bytes the calling thread has allocated so far, or -1 when the JVM cannot say — measurement
   * switched off after the probe opened, or the read failed. A frame with a -1 at either end
   * reports no allocation rather than a wrong one, and never throws into the method it observes.
   */
  long currentThreadBytes() {
    try {
      return threads.getThreadAllocatedBytes(Thread.currentThread().getId());
    } catch (Throwable unreadable) {
      return -1L;
    }
  }

  /**
   * The difference between two readings of a monotonic counter — allocated bytes here, collection
   * counts and milliseconds for {@link ProbeGc} — or -1 when either reading is unknown.
   */
  static long delta(long start, long end) {
    return start < 0L || end < 0L ? -1L : end - start;
  }
}
