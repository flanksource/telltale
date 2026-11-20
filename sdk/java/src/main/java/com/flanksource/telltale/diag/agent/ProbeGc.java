package com.flanksource.telltale.diag.agent;

import java.lang.management.GarbageCollectorMXBean;
import java.lang.management.ManagementFactory;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The JVM's garbage-collection counters, for a probe opened with {@code gc: true}.
 *
 * <p>Sums {@code getCollectionCount()} and {@code getCollectionTime()} over every
 * GarbageCollectorMXBean. These are JVM-wide, not per thread: a frame's delta is the collections
 * that ran anywhere in the JVM while it was open, and so the pause its wall time included. A
 * stop-the-world pause stalls every thread; a concurrent collector's time also counts phases that
 * ran beside the frame, which makes the figure an upper bound for such collectors.
 *
 * <p>One instance per probe opening. It also holds the counters at that opening, so a read can
 * report what the whole span collected, frame or no frame. The bean list is resolved once, on the
 * servlet thread that opens the probe.
 */
final class ProbeGc {

  private final GarbageCollectorMXBean[] collectors;
  private final long openCount;
  private final long openMillis;

  /** The counters when recording stopped, so a later read is not still counting; -1 until then. */
  private long endCount = -1L;

  private long endMillis = -1L;

  private ProbeGc(GarbageCollectorMXBean[] collectors) {
    this.collectors = collectors;
    this.openCount = collections();
    this.openMillis = collectionMillis();
  }

  /**
   * @throws IllegalStateException when this JVM reports no collectors, or a collector that cannot
   *     report its counters — either would make every frame report nothing.
   */
  static ProbeGc resolve() {
    List<GarbageCollectorMXBean> beans = ManagementFactory.getGarbageCollectorMXBeans();
    if (beans == null || beans.isEmpty()) {
      throw new IllegalStateException(
          "this JVM reports no GarbageCollectorMXBean, so there are no"
              + " collection counters to read");
    }
    ProbeGc gc = new ProbeGc(beans.toArray(new GarbageCollectorMXBean[0]));
    if (gc.openCount < 0L || gc.openMillis < 0L) {
      throw new IllegalStateException(
          "a GarbageCollectorMXBean on this JVM reports its collection count"
              + " or time as undefined (-1)");
    }
    return gc;
  }

  /** Collections so far, summed over every collector, or -1 when any cannot say. */
  long collections() {
    long total = 0L;
    try {
      for (GarbageCollectorMXBean collector : collectors) {
        long count = collector.getCollectionCount();
        if (count < 0L) {
          return -1L;
        }
        total += count;
      }
    } catch (Throwable unreadable) {
      return -1L;
    }
    return total;
  }

  /** Accumulated collection time in milliseconds, summed over every collector, or -1. */
  long collectionMillis() {
    long total = 0L;
    try {
      for (GarbageCollectorMXBean collector : collectors) {
        long millis = collector.getCollectionTime();
        if (millis < 0L) {
          return -1L;
        }
        total += millis;
      }
    } catch (Throwable unreadable) {
      return -1L;
    }
    return total;
  }

  /** Fixes the span's figure at the moment recording stopped. Idempotent. */
  synchronized void stop() {
    if (endCount >= 0L) {
      return;
    }
    endCount = collections();
    endMillis = collectionMillis();
  }

  /**
   * What the JVM collected since the probe opened — up to {@link #stop} once recording stopped — as
   * {@code {count, pauseMs}}; null when a counter could not be read, never zeros standing in for
   * unknown.
   */
  synchronized Map<String, Object> sinceOpen() {
    long count = endCount >= 0L ? endCount : collections();
    long millis = endCount >= 0L ? endMillis : collectionMillis();
    if (count < 0L || millis < 0L) {
      return null;
    }
    Map<String, Object> span = new LinkedHashMap<String, Object>();
    span.put("count", Long.valueOf(count - openCount));
    span.put("pauseMs", Long.valueOf(millis - openMillis));
    return span;
  }
}
