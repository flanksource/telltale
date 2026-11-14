package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.Probes;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * {@code gc: true} — the garbage collections that ran, JVM-wide, while a frame was open, read from
 * the GarbageCollectorMXBeans at its entry and exit.
 */
final class ProbeGcTest {

  @BeforeEach
  @AfterEach
  void isolate() {
    Probes.closeAll();
  }

  @Test
  void reportsTheCollectionsThatRanWhileAFrameWasOpen() {
    Probes.open("k", Probes.options("gc", Boolean.TRUE));
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    System.gc();
    DiagAgent.probeExit("k", frame);

    Map<String, Object> exit = Probes.first("k", "exit");
    assertTrue(
        number(exit, "gcCount") >= 1L,
        () -> "System.gc() ran a collection inside the frame: " + exit);
    assertTrue(
        number(exit, "gcPauseMs") >= 0L,
        () -> "the collection time travels with the count: " + exit);
    assertFalse(
        Probes.first("k", "enter").containsKey("gcCount"), "an entry has measured nothing yet");
  }

  @Test
  void carriesNoGcFieldsWhenTheProbeDidNotAskForThem() {
    Probes.open("k");
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    System.gc();
    DiagAgent.probeExit("k", frame);

    Map<String, Object> exit = Probes.first("k", "exit");
    assertFalse(
        exit.containsKey("gcCount") || exit.containsKey("gcPauseMs"),
        () -> "gc is opt-in: " + exit);
  }

  @Test
  void reportsTheCollectionsOfAFrameThatThrew() {
    Probes.open("k", Probes.options("gc", Boolean.TRUE));
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    System.gc();
    DiagAgent.probeThrow("k", frame, new IllegalStateException("boom"));

    assertTrue(number(Probes.first("k", "throw"), "gcCount") >= 1L);
  }

  /**
   * The span-level figure: every collection since the probe opened, whether or not a frame was
   * open.
   */
  @Test
  void reportsTheCollectionsSinceTheProbeOpenedOnEveryRead() {
    Probes.open("k", Probes.options("gc", Boolean.TRUE));
    System.gc();

    Map<String, Object> drained = DiagAgent.drain("k", 0L, 10);
    Object gc = drained.get("gc");
    assertTrue(
        gc instanceof Map, () -> "a gc probe's read carries the span's collections: " + drained);
    assertTrue(((Number) ((Map<?, ?>) gc).get("count")).longValue() >= 1L, String.valueOf(gc));
    assertNotNull(((Map<?, ?>) gc).get("pauseMs"), String.valueOf(gc));
  }

  @Test
  void leavesTheSpanFigureOffAReadOfAProbeWithoutGc() {
    Probes.open("k");

    assertFalse(DiagAgent.drain("k", 0L, 10).containsKey("gc"));
  }

  @Test
  void refusesAJoinWhoseGcDiffersFromTheOpenRing() {
    Probes.open("k", "3f|com.example.Outer", Probes.options("gc", Boolean.TRUE));
    Map<String, Object> join = new LinkedHashMap<String, Object>();
    join.put("join", Boolean.TRUE);

    Map<String, Object> opened = DiagAgent.openProbe("k", "3f|com.example.Inner", join);

    assertEquals(Boolean.FALSE, opened.get("opened"));
    assertTrue(
        String.valueOf(opened.get("error")).contains("gc"), String.valueOf(opened.get("error")));
  }

  @Test
  void saysTheRingMeasuresGcWhenItOpens() {
    Map<String, Object> opened =
        DiagAgent.openProbe("k", "test|k", Probes.options("gc", Boolean.TRUE));

    assertEquals(Boolean.TRUE, opened.get("gc"), String.valueOf(opened));
  }

  private static long number(Map<String, Object> event, String field) {
    Object value = event.get(field);
    if (!(value instanceof Number)) {
      throw new AssertionError("no " + field + " on " + event);
    }
    return ((Number) value).longValue();
  }
}
