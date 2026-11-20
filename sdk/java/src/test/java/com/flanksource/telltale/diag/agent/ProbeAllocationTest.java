package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.Probes;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * {@code alloc: true} — the bytes the calling thread allocated between a frame's entry and its
 * exit, read from the JVM's per-thread allocation counter.
 *
 * <p>Opt-in, because each reading is a call into the JVM on the observed thread at every entry and
 * exit; a probe that did not ask for it must carry nothing.
 */
final class ProbeAllocationTest {

  private static final int ONE_MEGABYTE = 1 << 20;

  /** Larger than any one string snapshot keeps, so rendering it allocates a capped copy. */
  private static final int LARGE_RETURN_CHARS = 200_000;

  /**
   * What the probe's own bookkeeping may add to a frame that allocated nothing: the enter event
   * and, on Java 8, the counter read's two small arrays.
   */
  private static final long PROBE_OVERHEAD_BYTES = 8_192L;

  private static byte[] sink;

  @BeforeEach
  @AfterEach
  void isolate() {
    Probes.closeAll();
    sink = null;
  }

  @Test
  void reportsTheBytesAFrameAllocatedOnItsExitAndNothingOnItsEntry() {
    Probes.open("k", Probes.options("alloc", Boolean.TRUE));
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    sink = new byte[ONE_MEGABYTE];
    DiagAgent.probeExit("k", frame);

    assertFalse(
        Probes.first("k", "enter").containsKey("allocBytes"), "an entry has measured nothing yet");
    assertTrue(
        allocBytes(Probes.first("k", "exit")) >= ONE_MEGABYTE,
        () -> "the exit carries at least the 1MB array: " + Probes.first("k", "exit"));
  }

  @Test
  void carriesNoAllocationWhenTheProbeDidNotAskForIt() {
    Probes.open("k");
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    sink = new byte[ONE_MEGABYTE];
    DiagAgent.probeExit("k", frame);

    assertFalse(
        Probes.first("k", "exit").containsKey("allocBytes"),
        () -> "alloc is opt-in: " + Probes.first("k", "exit"));
  }

  @Test
  void reportsTheAllocationOfAFrameThatThrew() {
    Probes.open("k", Probes.options("alloc", Boolean.TRUE));
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    sink = new byte[ONE_MEGABYTE];
    DiagAgent.probeThrow("k", frame, new IllegalStateException("boom"));

    assertTrue(
        allocBytes(Probes.first("k", "throw")) >= ONE_MEGABYTE,
        () ->
            "the throw closes the frame and carries what it allocated: "
                + Probes.first("k", "throw"));
  }

  @Test
  void includesWhatANestedFrameAllocatedInItsCallersBytes() {
    Probes.open("k", Probes.options("alloc", Boolean.TRUE));
    long outer = DiagAgent.probeEnter("k", "Fixture", "outer", "()V", 0, null);
    long inner = DiagAgent.probeEnter("k", "Fixture", "inner", "()V", 0, null);
    sink = new byte[ONE_MEGABYTE];
    DiagAgent.probeExit("k", inner);
    DiagAgent.probeExit("k", outer);

    long innerBytes = 0L;
    long outerBytes = 0L;
    for (Map<String, Object> event : Probes.events("k")) {
      if (!"exit".equals(event.get("kind"))) {
        continue;
      }
      long frame = ((Number) event.get("frame")).longValue();
      if (frame == inner) {
        innerBytes = allocBytes(event);
      } else if (frame == outer) {
        outerBytes = allocBytes(event);
      }
    }
    assertTrue(innerBytes >= ONE_MEGABYTE, "the inner frame allocated the array: " + innerBytes);
    assertTrue(outerBytes >= innerBytes, "the caller's bytes include its callee's: " + outerBytes);
  }

  /**
   * The counter is read in probeReturn BEFORE the value is snapshotted, so rendering the return
   * value is not charged to the method that returned it.
   */
  @Test
  void doesNotChargeRenderingTheReturnValueToTheFrame() {
    Probes.open("k", Probes.options("alloc", Boolean.TRUE));
    char[] chars = new char[LARGE_RETURN_CHARS];
    Arrays.fill(chars, 'x');
    String value = new String(chars);
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()Ljava/lang/String;", 0, null);
    DiagAgent.probeReturn("k", frame, value);
    DiagAgent.probeExit("k", frame);

    long measured = allocBytes(Probes.first("k", "exit"));
    assertTrue(
        measured < PROBE_OVERHEAD_BYTES,
        () ->
            "a frame that allocated nothing reports only the probe's own bookkeeping, not the"
                + " snapshot of its return value: "
                + measured);
  }

  /** A span arms every target the same way; a join that disagrees is refused rather than merged. */
  @Test
  void refusesAJoinWhoseAllocDiffersFromTheOpenRing() {
    Probes.open("k", "3f|com.example.Outer", Probes.options("alloc", Boolean.TRUE));
    Map<String, Object> join = new LinkedHashMap<String, Object>();
    join.put("join", Boolean.TRUE);

    Map<String, Object> opened = DiagAgent.openProbe("k", "3f|com.example.Inner", join);

    assertEquals(Boolean.FALSE, opened.get("opened"));
    assertNotNull(opened.get("error"));
    assertTrue(
        String.valueOf(opened.get("error")).contains("alloc"), String.valueOf(opened.get("error")));
  }

  @Test
  void saysTheRingMeasuresAllocationWhenItOpens() {
    Map<String, Object> opened =
        DiagAgent.openProbe("k", "test|k", Probes.options("alloc", Boolean.TRUE));

    assertEquals(Boolean.TRUE, opened.get("opened"), String.valueOf(opened));
    assertEquals(Boolean.TRUE, opened.get("alloc"), "the caller can see the request was honoured");
  }

  private static long allocBytes(Map<String, Object> event) {
    Object bytes = event.get("allocBytes");
    if (!(bytes instanceof Number)) {
      throw new AssertionError("no allocBytes on " + event);
    }
    return ((Number) bytes).longValue();
  }
}
