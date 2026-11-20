package com.flanksource.telltale.diag.agent;

import java.util.ArrayList;
import java.util.List;

/**
 * One filtered probe's root call in progress on one thread: the events it has recorded so far,
 * held until the call ends and its {@link ProbeFilter} decides whether they are published.
 *
 * <p>Bounded by the probe's own budgets — at most {@code events} events (and never more than the
 * ring holds) and its share of {@code budget-bytes} — because what sits here is heap the observed
 * JVM pays for. A call that outgrows the bound keeps its HEAD and its closing events and counts what
 * it {@link #omitted}: the root's entry, its first nested calls, its return and its exit are what a
 * reader of a slow or failing call needs, and dropping the whole call for being large would hide
 * exactly the calls {@code minCost} exists to find.
 *
 * <p>Confined to its thread; nothing here is synchronized.
 */
final class ProbePending {

  final ProbeRing ring;
  final long rootFrame;

  /** The thread's depth when the root was entered; a nested event is always deeper. */
  final int rootDepth;

  /**
   * The root's arguments already failed a predicate. Nothing more is held for it; it is counted as
   * {@code filtered} when it ends.
   */
  final boolean rejected;

  boolean returnMatched;
  int omitted;

  private final List<ProbeEvent> events = new ArrayList<ProbeEvent>();
  private final int maxEvents;
  private final long maxBytes;
  private long bytes;

  ProbePending(ProbeRing ring, long rootFrame, int rootDepth, boolean rejected) {
    this.ring = ring;
    this.rootFrame = rootFrame;
    this.rootDepth = rootDepth;
    this.rejected = rejected;
    this.maxEvents = Math.min(ring.config.maxEvents, ring.capacity);
    this.maxBytes = ring.budgetBytes();
  }

  /** Holds an event recorded inside the call, unless the bound is reached. */
  void hold(ProbeEvent event) {
    if (rejected) {
      return;
    }
    long charge = event.charge();
    if (events.size() >= maxEvents || bytes + charge > maxBytes) {
      omitted++;
      return;
    }
    events.add(event);
    bytes += charge;
  }

  /** Holds the root's own return or closing event, which is kept whatever the bound. */
  void close(ProbeEvent event) {
    if (!rejected) {
      events.add(event);
    }
  }

  /** Publishes the call, marking its last event with what was omitted from it. */
  void publish() {
    int last = events.size() - 1;
    for (int i = 0; i < last; i++) {
      ring.add(events.get(i));
    }
    if (last >= 0) {
      ring.add(omitted > 0 ? events.get(last).omitting(omitted) : events.get(last));
    }
    if (omitted > 0) {
      ring.omitted(omitted);
    }
  }
}
