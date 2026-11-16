package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.Probes;
import java.util.Collections;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * A capture is a sample, not a tap.
 *
 * <p>Left running, an instrumented method on a hot path pays the probe cost on every call for the
 * life of its lease and fills its ring with the last few seconds of a run nobody is reading. So a
 * probe records a bounded number of events and then stops — arthas' {@code -n}, defaulted here to
 * 100.
 *
 * <p>Stopping is not closing. Everything recorded stays readable, and the read says {@code
 * complete} so the span that opened it knows to disarm rather than concluding its recording was
 * revoked.
 */
final class ProbeEventBudgetTest {

  private static final String KEY = "budget-1a2b3c4d";

  @BeforeEach
  @AfterEach
  void isolate() {
    DiagAgent.closeAllProbes();
  }

  @Test
  void stopsRecordingOnceTheEventBudgetIsSpent() {
    Probes.open(KEY, Collections.<String, Object>singletonMap("events", Integer.valueOf(4)));

    for (int i = 0; i < 10; i++) {
      record();
    }

    assertEquals(
        4, Probes.events(KEY).size(), "a probe past its budget must record nothing further");
  }

  /**
   * A thousand, not arthas' hundred: the preservation run spent a hundred in the first 69ms of a
   * 35-iteration scan, before the method anyone was waiting for.
   */
  @Test
  void defaultsTheBudgetToOneThousandEvents() {
    Probes.open(KEY);

    for (int i = 0; i < 1500; i++) {
      record();
    }

    assertEquals(1000, Probes.events(KEY).size());
  }

  @Test
  void keepsWhatItRecordedRatherThanClosingTheBuffer() {
    Probes.open(KEY, Collections.<String, Object>singletonMap("events", Integer.valueOf(2)));
    record();
    record();
    record();

    Map<String, Object> read = DiagAgent.drain(KEY, 0L, Integer.MAX_VALUE);

    // active:false would tell the reader its recording was revoked and that an
    // empty capture says nothing about what ran. This capture is neither empty
    // nor revoked: it finished.
    assertEquals(Boolean.TRUE, read.get("active"));
    assertEquals(Boolean.FALSE, read.get("closed"));
    assertEquals(Boolean.TRUE, read.get("complete"));
    assertEquals(2, Probes.events(KEY).size());
  }

  @Test
  void reportsTheBudgetItWasGivenSoAReaderCanTellWhyItStopped() {
    Probes.open(KEY, Collections.<String, Object>singletonMap("events", Integer.valueOf(7)));

    Map<String, Object> read = DiagAgent.drain(KEY, 0L, Integer.MAX_VALUE);

    assertEquals(Integer.valueOf(7), read.get("maxEvents"));
    assertEquals(Boolean.FALSE, read.get("complete"));
  }

  /**
   * The budget bounds what is WRITTEN; the ring bounds what is KEPT. A budget larger than the ring
   * still stops at the budget, having evicted the start.
   */
  @Test
  void countsEventsWrittenRatherThanEventsHeld() {
    Map<String, Object> options = new java.util.LinkedHashMap<String, Object>();
    options.put("events", Integer.valueOf(12));
    options.put("ring", Integer.valueOf(4));
    Probes.open(KEY, options);

    for (int i = 0; i < 30; i++) {
      record();
    }

    Map<String, Object> read = DiagAgent.drain(KEY, 0L, Integer.MAX_VALUE);
    assertEquals(Boolean.TRUE, read.get("complete"));
    assertEquals(12L, ((Number) read.get("writeSeq")).longValue());
    assertEquals(4, Probes.events(KEY).size(), "the ring still only keeps its own capacity");
    assertFalse(Probes.events(KEY).isEmpty());
    assertTrue(((Number) read.get("dropped")).longValue() > 0);
  }

  /** One event, through the same entry point an instrumented method calls. */
  private static void record() {
    DiagAgent.probeEnter(
        KEY,
        "com.flanksource.telltale.diag.fixture.ProbeFixture",
        "greet",
        "(Ljava/lang/String;)Ljava/lang/String;",
        0,
        null);
  }
}
