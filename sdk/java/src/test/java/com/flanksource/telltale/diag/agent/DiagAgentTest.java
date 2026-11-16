package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.Probes;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * The agent-side half of instrumentation: a bounded buffer that reports what it dropped, and a
 * lease that a dead client cannot hold forever.
 *
 * <p>Both exist because the code here runs inside a JVM that did not ask for it. An unbounded
 * buffer on a hot method would exhaust the heap of the system being diagnosed, and a patch with no
 * deadline outlives the CLI that made it.
 */
final class DiagAgentTest {

  private static final String KEY = "3f|com.flanksource.telltale.diag.fixture.ProbeFixture";

  private DiagAgent.Restorer previous;

  @BeforeEach
  void isolate() {
    previous = DiagAgent.RESTORER;
    DiagAgent.closeAllProbes();
    DiagAgent.LEASES.clear();
  }

  @AfterEach
  void restore() {
    DiagAgent.RESTORER = previous;
    DiagAgent.closeAllProbes();
    DiagAgent.LEASES.clear();
  }

  /**
   * The buffer keeps the most RECENT events and says how many scrolled past.
   *
   * <p>This is the inversion that matters. A buffer that stops recording once full freezes on the
   * first N events of a span and then reports nothing about the part anyone was waiting for; a ring
   * keeps the newest window. Either way the loss has to be visible — a truncated capture that says
   * nothing reads as "the method only ran twice", and a reader cannot tell a quiet method from a
   * lost one.
   */
  @Test
  void keepsTheMostRecentWindowAndReportsWhatScrolledPast() {
    Probes.open("k", Probes.options("ring", 8));
    for (int i = 0; i < 15; i++) {
      DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    }

    Probes.Batch batch = Probes.batch("k", 0L);
    assertEquals(8, batch.events.size(), "the ring holds its capacity, not the whole run");
    assertEquals(7L, batch.dropped, "15 events through 8 slots leaves the first 7 gone");
    assertEquals(
        7L,
        ((Number) batch.events.get(0).get("seq")).longValue(),
        "what survives is the newest window, not the oldest");
  }

  /** A reader that keeps up is told it missed nothing, on every read. */
  @Test
  void reportsNothingDroppedForAReaderThatKeepsUp() {
    Probes.open("k", Probes.options("ring", 8));
    long cursor = 0L;
    for (int round = 0; round < 5; round++) {
      DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
      Probes.Batch batch = Probes.batch("k", cursor);
      assertEquals(0L, batch.dropped, "a reader inside the window loses nothing");
      assertEquals(1, batch.events.size());
      cursor = batch.nextSeq;
    }
  }

  @Test
  void handsBackTheValueItRecordedSoTheMethodsOwnReturnIsUnchanged() {
    Probes.open("k");
    Dcl value = new Dcl("ACT-1");
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);

    assertTrue(value == DiagAgent.probeReturn("k", frame, value));
    Map<String, Object> returned = Probes.node(Probes.first("k", "return").get("value"));
    assertEquals("return", returned.get("label"));
    assertEquals("bean", returned.get("kind"));
    assertEquals(Dcl.class.getName(), returned.get("type"));
    assertEquals("ACT-1", Probes.child(returned, "activityGuid").get("value"));
  }

  /**
   * Arguments arrive as object graphs: a DCL shows its fields, and a list of rows shows its rows.
   * The capture this replaces said {@code ActivityDcl@d7c405bf} and {@code
   * [[Ljava.lang.Object;@143c0b1}.
   */
  @Test
  void rendersEachArgumentAsAnObjectGraphLabelledByPosition() {
    Probes.open("k");
    List<Object[]> rows = new ArrayList<Object[]>();
    rows.add(new Object[] {"ACT-1", 1});
    DiagAgent.probeEnter(
        "k",
        "Fixture",
        "run",
        "(Ljava/lang/Object;Ljava/util/List;)V",
        0,
        new Object[] {new Dcl("ACT-2"), rows});

    List<Map<String, Object>> args = Probes.args(Probes.first("k", "enter"));
    assertEquals(2, args.size());
    assertEquals("arg0", args.get(0).get("label"));
    assertEquals("ACT-2", Probes.child(args.get(0), "activityGuid").get("value"));
    assertEquals("arg1 [1]", args.get(1).get("label"));
    assertEquals("list", args.get(1).get("kind"));
  }

  /**
   * A probe runs on application's own thread, so rendering what it saw must not run application's
   * code: a toString() can fire a lazy-load query, and one that throws or reaches another probe
   * would become a failure of the observed method.
   */
  @Test
  void neverCallsTheApplicationsOwnCodeToRenderAValue() {
    Probes.open("k");
    Dcl loud = new Dcl("ACT-3");
    loud.loud = true;
    DiagAgent.probeEnter("k", "Fixture", "run", "(Ljava/lang/Object;)V", 0, new Object[] {loud});

    assertEquals(0, loud.calls, "no toString, hashCode, equals or getter may run");
    assertEquals(
        "ACT-3",
        Probes.child(Probes.args(Probes.first("k", "enter")).get(0), "activityGuid").get("value"));
    assertEquals(0L, Probes.batch("k", 0L).suppressed);
  }

  /** arg-mode type reads nothing from the value: its class and identity only. */
  @Test
  void recordsOnlyTheClassAndIdentityInTypeMode() {
    Probes.open("k", Probes.options("arg-mode", "type"));
    DiagAgent.probeEnter(
        "k", "Fixture", "run", "(Ljava/lang/Object;)V", 0, new Object[] {new Dcl("ACT-4")});

    Map<String, Object> arg = Probes.args(Probes.first("k", "enter")).get(0);
    assertEquals("opaque", arg.get("kind"));
    assertEquals(Dcl.class.getName(), arg.get("type"));
    assertTrue(
        String.valueOf(arg.get("raw")).startsWith(Dcl.class.getName() + "@"), String.valueOf(arg));
    assertNull(arg.get("children"));
  }

  /**
   * Truncation is visible, because a silent cut reads as the whole value, and it keeps the tail: a
   * SQL statement's WHERE clause is at the end.
   */
  @Test
  void truncatesALongArgumentAtHeadAndTailAndSaysHowMuchItCut() {
    Probes.open("k", Probes.options("arg-chars", 16));
    StringBuilder long_ = new StringBuilder();
    for (int i = 0; i < 100; i++) {
      long_.append('x');
    }
    long_.append("WHERE");
    DiagAgent.probeEnter(
        "k", "Fixture", "run", "(Ljava/lang/Object;)V", 0, new Object[] {long_.toString()});

    Map<String, Object> enter = Probes.first("k", "enter");
    String rendered = String.valueOf(Probes.args(enter).get(0).get("value"));
    assertEquals("xxxxxxxx…(+89 chars)…xxxWHERE", rendered);
    assertEquals(Boolean.TRUE, enter.get("truncated"));
  }

  /** Past max-args the remaining arguments are counted, not dropped silently. */
  @Test
  void countsTheArgumentsPastTheArgumentCap() {
    Probes.open("k", Probes.options("max-args", 1));
    DiagAgent.probeEnter("k", "Fixture", "run", "(II)V", 0, new Object[] {1, 2});

    List<Map<String, Object>> args = Probes.args(Probes.first("k", "enter"));
    assertEquals("1", args.get(0).get("value"));
    assertEquals("(truncated)", args.get(1).get("label"));
    assertEquals("1 more args", args.get(1).get("value"));
  }

  /**
   * The line a call site was made from rides on its entry, so the viewer can show the caller's
   * code; a method's own entry has none and says nothing.
   */
  @Test
  void recordsTheCallSiteLineOnTheEntryOnly() {
    Probes.open("k");
    long frame = DiagAgent.probeEnter("k", "Fixture", "callee", "()V", 2935, null);
    DiagAgent.probeExit("k", frame);
    DiagAgent.probeEnter("k", "Fixture", "entry", "()V", 0, null);

    List<Map<String, Object>> events = Probes.events("k");
    assertEquals(2935, ((Number) events.get(0).get("line")).intValue());
    assertNull(events.get(1).get("line"), "an exit carries no line");
    assertNull(events.get(2).get("line"), "a method's own entry has no call-site line");
  }

  /** An exception's text is capped like any value, and the cut is flagged on ITS event. */
  @Test
  void flagsATruncatedExceptionOnTheThrowEventItself() {
    Probes.open("k", Probes.options("arg-chars", 16));
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    DiagAgent.probeThrow(
        "k", frame, new IllegalStateException("a message far longer than sixteen chars"));

    Map<String, Object> thrown = Probes.first("k", "throw");
    assertTrue(String.valueOf(thrown.get("error")).contains("chars)"), String.valueOf(thrown));
    assertEquals(Boolean.TRUE, thrown.get("truncated"));
  }

  /**
   * probeThrow runs on application's thread as the exception leaves, so a subclass's getMessage() —
   * application's code, which may look up a message catalogue or reach another instrumented method
   * — must not run. The message is read from the field instead, and the class alone still says what
   * was thrown.
   */
  @Test
  void describesAThrownExceptionWithoutCallingItsOwnGetMessage() {
    Probes.open("k");
    LoudError error = new LoudError("kept");
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    DiagAgent.probeThrow("k", frame, error);

    String described = String.valueOf(Probes.first("k", "throw").get("error"));
    assertEquals(0, error.calls, "getMessage() ran");
    assertTrue(described.startsWith(LoudError.class.getName()), described);
    assertFalse(described.contains("overridden"), described);
  }

  @Test
  void reportsAnEntryWithoutArgumentsWhenNoneWereAskedFor() {
    Probes.open("k");
    DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);

    assertNull(Probes.first("k", "enter").get("args"));
    assertEquals("Fixture", Probes.first("k", "enter").get("class"));
    assertEquals("run", Probes.first("k", "enter").get("method"));
  }

  /**
   * Depth returns to zero when a method throws, so a pooled thread does not carry a permanent
   * offset into every later trace that runs on it.
   */
  @Test
  void closesTheFrameWhenTheMethodThrewSoDepthDoesNotDrift() {
    Probes.open("k");
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    DiagAgent.probeThrow("k", frame, new IllegalStateException("boom"));

    long next = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    assertEquals(
        0,
        ((Number) Probes.events("k").get(2).get("depth")).intValue(),
        "the next call is outermost again");
    DiagAgent.probeExit("k", next);
  }

  /**
   * An inner frame that was never closed is unwound by the next outer exit rather than left to
   * raise the depth forever, and the repair is reported.
   */
  @Test
  void unwindsToTheNamedFrameWhenAnInnerExitWasLost() {
    Probes.open("k");
    long outer = DiagAgent.probeEnter("k", "Fixture", "outer", "()V", 0, null);
    DiagAgent.probeEnter("k", "Fixture", "inner", "()V", 0, null);
    DiagAgent.probeExit("k", outer);

    Map<String, Object> exit = Probes.first("k", "exit");
    assertEquals(
        1,
        ((Number) exit.get("unwound")).intValue(),
        "the lost inner frame is unwound and reported, not hidden");
    assertEquals(0, ((Number) exit.get("depth")).intValue());
  }

  /** Nesting on one thread is what depth and parent are for. */
  @Test
  void tracksDepthAndParentAcrossNestedFrames() {
    Probes.open("k");
    long a = DiagAgent.probeEnter("k", "Fixture", "a", "()V", 0, null);
    long b = DiagAgent.probeEnter("k", "Fixture", "b", "()V", 0, null);
    DiagAgent.probeEnter("k", "Fixture", "c", "()V", 0, null);

    List<Map<String, Object>> events = Probes.events("k");
    assertEquals(0, ((Number) events.get(0).get("depth")).intValue());
    assertEquals(1, ((Number) events.get(1).get("depth")).intValue());
    assertEquals(2, ((Number) events.get(2).get("depth")).intValue());
    assertEquals(a, ((Number) events.get(1).get("parent")).longValue());
    assertEquals(b, ((Number) events.get(2).get("parent")).longValue());
  }

  /**
   * The descriptor rides on the entry and nowhere else.
   *
   * <p>An entry is what carries an invocation's identity; the events that close the frame are tied
   * to it by {@code (threadId, frame)} and already resolve through it. Repeating the descriptor on
   * each of them would be three more copies of the same string per invocation, charged against a
   * buffer whose whole design is a byte budget, for nothing a reader could not already get.
   */
  @Test
  void namesTheOverloadOnTheEntryAndLeavesTheRestTiedToItByFrame() {
    Probes.open("k");
    long frame = DiagAgent.probeEnter("k", "Fixture", "describe", "(I)Ljava/lang/String;", 0, null);
    DiagAgent.probeExit("k", frame);

    assertEquals(
        "(I)Ljava/lang/String;",
        Probes.first("k", "enter").get("descriptor"),
        "the entry says which overload ran, which its method name cannot");
    assertNull(Probes.first("k", "exit").get("descriptor"));
  }

  /** The cap refuses openly, naming what is already open so one can be closed. */
  @Test
  void refusesToOpenMoreProbeKeysThanTheCapAllows() {
    Map<String, Object> options = Probes.options("keys", 1);
    Probes.open("first", options);

    Map<String, Object> refused = DiagAgent.openProbe("second", "owner", options);
    assertEquals(Boolean.FALSE, refused.get("opened"));
    assertTrue(
        String.valueOf(refused.get("error")).contains("first"),
        () -> "the refusal must name what is open: " + refused.get("error"));
  }

  /**
   * A probe closed while a frame is still open must not throw into the method that is mid-flight —
   * closing is an immediate stop, not a fault.
   */
  @Test
  void neverThrowsWhenTheRingWasClosedMidFlight() {
    Probes.open("k");
    long frame = DiagAgent.probeEnter("k", "Fixture", "run", "()V", 0, null);
    DiagAgent.closeProbe("k");

    DiagAgent.probeExit("k", frame);
    DiagAgent.probeThrow("k", frame, new IllegalStateException("boom"));
    assertEquals(
        "value",
        DiagAgent.probeReturn("k", frame, "value"),
        "the value still comes back after the probe is gone");
  }

  @Test
  void restoresAPatchWhoseLeaseHasLapsedAndForgetsIt() {
    List<String> restored = new ArrayList<String>();
    DiagAgent.RESTORER =
        new DiagAgent.Restorer() {
          @Override
          public void restore(String key) {
            restored.add(key);
          }
        };
    DiagAgent.lease(KEY, -1L);

    DiagAgent.reapExpired();

    assertEquals(1, restored.size());
    assertEquals(KEY, restored.get(0));
    assertNull(DiagAgent.LEASES.get(KEY), "a reaped lease must not be reaped again");
  }

  @Test
  void leavesALiveLeaseAlone() {
    DiagAgent.RESTORER =
        new DiagAgent.Restorer() {
          @Override
          public void restore(String key) {
            throw new AssertionError("a live lease must not be restored: " + key);
          }
        };
    DiagAgent.lease(KEY, 60_000L);

    DiagAgent.reapExpired();

    assertTrue(DiagAgent.LEASES.containsKey(KEY));
  }

  /**
   * A class that cannot be restored — its loader went with a webapp redeploy — must not be retried
   * every five seconds for the life of the JVM, and must not strand every other lease behind it.
   */
  @Test
  void dropsALeaseItCannotRestoreRatherThanRetryingForever() {
    final List<String> attempted = new ArrayList<String>();
    DiagAgent.RESTORER =
        new DiagAgent.Restorer() {
          @Override
          public void restore(String key) throws Exception {
            attempted.add(key);
            throw new IllegalStateException("class not loaded");
          }
        };
    DiagAgent.lease(KEY, -1L);
    DiagAgent.lease("other|com.example.Other", -1L);

    DiagAgent.reapExpired();

    assertEquals(2, attempted.size(), "one failure must not stop the rest");
    assertTrue(DiagAgent.LEASES.isEmpty());
  }

  @Test
  void doesNothingUntilTheWarHasSaidHowToRestore() {
    DiagAgent.RESTORER = null;
    DiagAgent.lease(KEY, -1L);

    DiagAgent.reapExpired();

    assertFalse(DiagAgent.LEASES.isEmpty(), "the lease survives until something can act on it");
  }

  /** A DCL-shaped value that counts every call into its own code. */
  static final class LoudError extends RuntimeException {
    int calls;

    LoudError(String message) {
      super(message);
    }

    @Override
    public String getMessage() {
      calls++;
      return "overridden";
    }
  }

  static final class Dcl {
    final String activityGuid;
    boolean loud;
    int calls;

    Dcl(String activityGuid) {
      this.activityGuid = activityGuid;
    }

    public String getActivityGuid() {
      calls++;
      return activityGuid;
    }

    @Override
    public String toString() {
      calls++;
      if (loud) {
        throw new IllegalStateException("toString ran");
      }
      return "Dcl";
    }

    @Override
    public int hashCode() {
      calls++;
      return 1;
    }

    @Override
    public boolean equals(Object other) {
      calls++;
      return other == this;
    }
  }
}
