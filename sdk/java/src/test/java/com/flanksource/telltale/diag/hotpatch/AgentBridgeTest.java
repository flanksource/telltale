package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.agent.DiagAgent;
import com.flanksource.telltale.diag.fixture.Probes;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * The WAR reading the agent's state across a classloader boundary.
 *
 * <p>In production those are two different loaders — the agent jar sits on the system class path so
 * it survives a WAR redeploy, and everything crossing between them is a JDK type for exactly that
 * reason. Here they are the same loader, so what these exercise is the reflection itself: the field
 * names, the drain semantics, and the {@link java.lang.reflect.Proxy} that lets this WAR implement
 * an interface it cannot name.
 */
final class AgentBridgeTest {

  /**
   * The options {@code ClassesServlet.instrument} opens every probe with: the probe body plus the
   * selector it was armed for. A ring opened any other way does not happen in production, so these
   * tests do not open one either.
   */
  private static Map<String, Object> armedFor(String method, boolean calls) {
    Map<String, Object> options = new LinkedHashMap<String, Object>();
    options.put("method", method);
    if (calls) {
      options.put("calls", Boolean.TRUE);
    }
    return options;
  }

  /** What a span passes when it is arming another method into a ring it owns. */
  private static Map<String, Object> joining(String method) {
    Map<String, Object> options = armedFor(method, false);
    options.put("join", Boolean.TRUE);
    return options;
  }

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
   * A cursor read is non-destructive, which is the whole contract change: two readers can watch one
   * span at once, and an event written between a read and a removal can no longer vanish with
   * nothing to show it existed.
   */
  @Test
  void readsTheSameBatchTwiceForOneCursorBecauseAReadRemovesNothing() {
    Probes.open("K.m.1");
    record("K.m.1", 2);

    Map<String, Object> first = AgentBridge.drain("K.m.1", 0L, 100);
    Map<String, Object> second = AgentBridge.drain("K.m.1", 0L, 100);

    assertEquals(2, ((List<?>) first.get("events")).size());
    assertEquals(
        first.get("events"),
        second.get("events"),
        "the same cursor must see the same events; nothing was consumed");
    assertEquals(2L, ((Number) first.get("nextSeq")).longValue());
    assertEquals(0L, ((Number) first.get("dropped")).longValue());
  }

  /** Advancing the cursor is how a reader gets only what is new. */
  @Test
  void returnsOnlyWhatIsPastTheCursor() {
    Probes.open("K.m.4");
    record("K.m.4", 3);

    Map<String, Object> tail = AgentBridge.drain("K.m.4", 2L, 100);

    assertEquals(1, ((List<?>) tail.get("events")).size(), "one event lies past sequence 2");
    assertEquals(3L, ((Number) tail.get("nextSeq")).longValue());
  }

  @Test
  void freezesAProbeBeforeDrainingWithoutDiscardingItsEvents() {
    Probes.open("K.m.freeze");
    record("K.m.freeze", 2);

    Map<String, Object> frozen = AgentBridge.freezeProbe("K.m.freeze");
    record("K.m.freeze", 1);
    Map<String, Object> read = AgentBridge.drain("K.m.freeze", 0L, 100);
    Map<String, Object> repeated = AgentBridge.freezeProbe("K.m.freeze");

    assertEquals(Boolean.TRUE, frozen.get("frozen"));
    assertEquals(Boolean.TRUE, frozen.get("changed"));
    assertEquals(Boolean.FALSE, read.get("active"));
    assertEquals(
        2,
        ((List<?>) read.get("events")).size(),
        "freeze keeps the completed prefix and rejects later writes");
    assertEquals(2L, ((Number) read.get("writeSeq")).longValue());
    assertEquals(Boolean.TRUE, repeated.get("frozen"));
    assertEquals(Boolean.FALSE, repeated.get("changed"), "freeze is idempotent");
  }

  /**
   * A cursor the ring has already evicted past is told how much it missed, rather than being
   * resumed quietly at the oldest surviving event.
   */
  @Test
  void reportsWhatTheRingEvictedRatherThanResumingSilently() {
    Probes.open("K.m.5", Probes.options("ring", 4));
    record("K.m.5", 10);

    Map<String, Object> read = AgentBridge.drain("K.m.5", 0L, 100);

    assertEquals(
        6L,
        ((Number) read.get("dropped")).longValue(),
        "10 events through 4 slots leaves the first 6 gone");
    assertEquals(4, ((List<?>) read.get("events")).size());
  }

  /** Closing frees the buffer; nothing else does, and abandoned keys are never revisited. */
  @Test
  void closesEveryProbeOwnedByOneClassAndLeavesAnotherOwnersAlone() {
    DiagAgent.openProbe("A.m.1", "3f|com.example.A", armedFor("m", false));
    DiagAgent.openProbe("A.m.2", "3f|com.example.A", armedFor("m", false));
    DiagAgent.openProbe("B.m.1", "3f|com.example.B", armedFor("m", false));

    List<Map<String, Object>> closed = AgentBridge.closeProbesOwnedBy("3f", "com.example.A");

    assertEquals(2, closed.size(), "both of A's probes close together");
    assertEquals(1, AgentBridge.probeKeys().size(), "B's probe is untouched");
    assertEquals("B.m.1", AgentBridge.probeKeys().get(0).get("key"));
  }

  /**
   * A span that instruments several classes records them all under ONE key, so the ring is owned by
   * every class it captures on rather than by whichever one armed it first.
   *
   * <p>Restoring one of them must not take the capture down. The other classes are still
   * instrumented and still running rewritten bytecode; closing the ring under them means probeEnter
   * early-returns and the span goes silent with no counter moving — the failure that reads as "the
   * method never ran".
   */
  @Test
  void keepsASharedProbeOpenUntilTheLastClassItCapturesOnIsRestored() {
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", armedFor("m", false));
    DiagAgent.openProbe("Span.m.1", "3f|com.example.B", joining("m"));
    DiagAgent.openProbe("Span.m.1", "3f|com.example.C", joining("m"));

    assertEquals(1, AgentBridge.probeKeys().size(), "one span, one ring");

    assertTrue(
        AgentBridge.closeProbesOwnedBy("3f", "com.example.A").isEmpty(),
        "restoring one class of a set closes nothing");
    assertEquals(1, AgentBridge.probeKeys().size(), "B and C are still capturing");

    AgentBridge.closeProbesOwnedBy("3f", "com.example.B");
    assertEquals(1, AgentBridge.probeKeys().size(), "C is still capturing");

    List<Map<String, Object>> closed = AgentBridge.closeProbesOwnedBy("3f", "com.example.C");

    assertEquals(1, closed.size(), "the last owner closes it");
    assertTrue(AgentBridge.probeKeys().isEmpty());
  }

  /** Re-arming the same class under one key is not a second owner to wait for. */
  @Test
  void countsEachOwningClassOnce() {
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", armedFor("m", false));
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", joining("m"));

    assertEquals(
        1,
        AgentBridge.closeProbesOwnedBy("3f", "com.example.A").size(),
        "one class restored, one ring closed — not a ring left waiting on a phantom owner");
    assertTrue(AgentBridge.probeKeys().isEmpty());
  }

  /**
   * Sharing a ring is something a span asks for, never something it falls into.
   *
   * <p>Two spans that computed the same key would otherwise pool their events into one buffer, and
   * each would read the other's calls as its own — a capture that is wrong while looking entirely
   * healthy. Only the span arming its second method knows it means to share, so only it may say so.
   */
  @Test
  void refusesASecondSpanOnALiveKeyRatherThanPoolingTwoCapturesIntoOne() {
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", armedFor("m", false));

    Map<String, Object> refused = DiagAgent.openProbe("Span.m.1", "3f|com.example.B", null);

    assertEquals(Boolean.FALSE, refused.get("opened"));
    assertTrue(
        String.valueOf(refused.get("error")).contains("join"),
        () -> "the refusal must name the way through: " + refused.get("error"));
    assertEquals(
        1,
        ((List<?>) AgentBridge.probeKeys().get(0).get("owners")).size(),
        "the refused caller did not become an owner the close path would wait on");
  }

  /**
   * The reverse of {@code owners}: which probes still record on one class.
   *
   * <p>This is what a restore has to ask before it puts a class's original bytes back. Three spans
   * on one class is not a corner case — the preservation plan instruments {@code ActivityDal} three
   * ways in one step — and without this question the first span to finish silently disarms the
   * other two, then they fail to restore a class that is no longer patched.
   */
  @Test
  void reportsWhichProbesStillRecordOnAClassSoARestoreKnowsWhoElseItWouldDisarm() {
    DiagAgent.openProbe("A.findNext.1", "3f|com.example.A", null);
    DiagAgent.openProbe("A.buildDcl.2", "3f|com.example.A", null);
    DiagAgent.openProbe("B.other.3", "3f|com.example.B", null);

    List<String> holding = AgentBridge.probeKeysOwnedBy("3f", "com.example.A");

    assertEquals(2, holding.size(), () -> "both of A's spans hold it: " + holding);
    assertTrue(holding.contains("A.findNext.1"));
    assertTrue(holding.contains("A.buildDcl.2"));
    assertTrue(AgentBridge.probeKeysOwnedBy("3f", "com.example.B").contains("B.other.3"));
    assertTrue(
        AgentBridge.probeKeysOwnedBy("3f", "com.example.Never").isEmpty(),
        "a class nothing records on holds nothing");
  }

  /**
   * One span finishing releases only its own claim. The others keep recording, which is the whole
   * point: their methods are still rewritten, and a ring closed under them means probeEnter
   * early-returns and the span reports a method that never ran.
   */
  @Test
  void releasesOneSpansClaimAndLeavesTheOtherSpansRingOpen() {
    DiagAgent.openProbe("A.findNext.1", "3f|com.example.A", null);
    DiagAgent.openProbe("A.buildDcl.2", "3f|com.example.A", null);

    Map<String, Object> released = AgentBridge.releaseProbe("A.findNext.1", "3f", "com.example.A");

    assertEquals(Boolean.TRUE, released.get("released"));
    assertEquals(Boolean.TRUE, released.get("closed"), "its own ring is done and freed");
    assertEquals(
        Collections.singletonList("A.buildDcl.2"),
        AgentBridge.probeKeysOwnedBy("3f", "com.example.A"),
        "the other span still holds the class, so its bytes must stay");
  }

  /** The last span to let go is the one that leaves the class held by nothing. */
  @Test
  void reportsNoRemainingHolderOnceTheLastSpanHasReleased() {
    DiagAgent.openProbe("A.findNext.1", "3f|com.example.A", null);
    DiagAgent.openProbe("A.buildDcl.2", "3f|com.example.A", null);

    AgentBridge.releaseProbe("A.findNext.1", "3f", "com.example.A");
    AgentBridge.releaseProbe("A.buildDcl.2", "3f", "com.example.A");

    assertTrue(
        AgentBridge.probeKeysOwnedBy("3f", "com.example.A").isEmpty(),
        "nothing records on it any more, so now the original bytes go back");
    assertTrue(AgentBridge.probeKeys().isEmpty());
  }

  /**
   * A span spanning several classes releases one of them at a time, so its ring outlives the first
   * release exactly as {@code closeProbesOwnedBy} arranges.
   */
  @Test
  void keepsASharedRingOpenWhenOnlyOneOfItsClassesIsReleased() {
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", armedFor("m", false));
    DiagAgent.openProbe("Span.m.1", "3f|com.example.B", joining("m"));

    Map<String, Object> released = AgentBridge.releaseProbe("Span.m.1", "3f", "com.example.A");

    assertEquals(Boolean.TRUE, released.get("released"));
    assertEquals(Boolean.FALSE, released.get("closed"), "B is still being captured through it");
    assertTrue(AgentBridge.probeKeysOwnedBy("3f", "com.example.A").isEmpty());
    assertEquals(
        Collections.singletonList("Span.m.1"), AgentBridge.probeKeysOwnedBy("3f", "com.example.B"));
  }

  /**
   * Releasing a key that is not open, or one that never recorded on this class, says so rather than
   * reporting a release that did not happen — a caller uses this to decide whether to put a class's
   * bytes back.
   */
  @Test
  void separatesAReleaseThatHappenedFromOneWithNothingToRelease() {
    DiagAgent.openProbe("A.findNext.1", "3f|com.example.A", null);

    assertEquals(
        Boolean.FALSE,
        AgentBridge.releaseProbe("A.never.9", "3f", "com.example.A").get("released"),
        "a key that was never open released nothing");
    assertEquals(
        Boolean.FALSE,
        AgentBridge.releaseProbe("A.findNext.1", "3f", "com.example.Other").get("released"),
        "a live key that does not record on that class released nothing");
    assertEquals(
        Collections.singletonList("A.findNext.1"),
        AgentBridge.probeKeysOwnedBy("3f", "com.example.A"),
        "and neither non-release disturbed the real holder");
  }

  /** The keys listing reports every owner, so a reader can see what a span spans. */
  @Test
  void reportsEveryClassASharedProbeCapturesOn() {
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", armedFor("m", false));
    DiagAgent.openProbe("Span.m.1", "3f|com.example.B", joining("m"));

    Object owners = AgentBridge.probeKeys().get(0).get("owners");

    assertTrue(
        owners instanceof List, "owners is a list, not the first owner wearing the old name");
    assertEquals(2, ((List<?>) owners).size());
    assertTrue(((List<?>) owners).contains("3f|com.example.A"));
    assertTrue(((List<?>) owners).contains("3f|com.example.B"));
  }

  /**
   * Each key carries what every one of its owners was armed for — the selector and whether it
   * records the call tree.
   *
   * <p>The ring alone cannot say whether it is a watch or a trace, and a reader annotating a call
   * tree with "this method is being traced" has no other source for it: the arming response is gone
   * once the page that armed it is.
   */
  @Test
  void reportsEachOwnersArmingSoAReaderCanTellAWatchFromATrace() {
    DiagAgent.openProbe("Dal.find.1", "3f|com.example.Dal", armedFor("find", false));
    DiagAgent.openProbe("Dal.build.2", "3f|com.example.Dal", armedFor("build(*)", true));
    DiagAgent.openProbe("Dal.build.2", "3f|com.example.Util", joining("append"));

    Map<String, Object> watch = keyed("Dal.find.1");
    Map<String, Object> trace = keyed("Dal.build.2");

    assertEquals(
        Collections.singletonList(arming("3f|com.example.Dal", "find", false)),
        watch.get("armings"));
    assertEquals(
        Arrays.asList(
            arming("3f|com.example.Dal", "build(*)", true),
            arming("3f|com.example.Util", "append", false)),
        trace.get("armings"));
  }

  /**
   * Only the owners still holding the ring are reported. A released class's arming describes a
   * method that no longer records into this key, and annotating it as recording would be a claim
   * about bytes that went back.
   */
  @Test
  void omitsTheArmingOfAClassTheSharedRingHasReleased() {
    DiagAgent.openProbe("Span.m.1", "3f|com.example.A", armedFor("a", true));
    DiagAgent.openProbe("Span.m.1", "3f|com.example.B", joining("b"));

    AgentBridge.releaseProbe("Span.m.1", "3f", "com.example.A");

    assertEquals(
        Collections.singletonList(arming("3f|com.example.B", "b", false)),
        keyed("Span.m.1").get("armings"));
  }

  /**
   * Every ring {@code instrument} opens carries its selector. One without it is a defect in
   * whatever opened it, and reporting the key with a blank method would let a reader match it
   * against nothing and call the class unwatched.
   */
  @Test
  void refusesToReportAnArmingThatNamesNoMethod() {
    DiagAgent.openProbe("A.m.1", "3f|com.example.A", null);

    IllegalStateException refused =
        assertThrows(IllegalStateException.class, AgentBridge::probeKeys);

    assertTrue(
        refused.getMessage().contains("A.m.1") && refused.getMessage().contains("3f|com.example.A"),
        () -> "the refusal must name the key and owner: " + refused.getMessage());
  }

  private static Map<String, Object> keyed(String key) {
    for (Map<String, Object> row : AgentBridge.probeKeys()) {
      if (key.equals(row.get("key"))) {
        return row;
      }
    }
    throw new AssertionError("no open probe " + key + " in " + AgentBridge.probeKeys());
  }

  private static Map<String, Object> arming(String owner, String method, boolean calls) {
    Map<String, Object> row = new LinkedHashMap<String, Object>();
    row.put("owner", owner);
    row.put("method", method);
    row.put("calls", Boolean.valueOf(calls));
    return row;
  }

  /** Records {@code count} entry events under an already-open key. */
  private static void record(String key, int count) {
    for (int i = 0; i < count; i++) {
      DiagAgent.probeEnter(key, "com.example.K", "m", "()V", 0, null);
    }
  }

  /**
   * A key that recorded nothing is an answer, not an error — the method may simply not have run. A
   * key that was never opened at all is different, and says so, because the two have different
   * fixes.
   */
  @Test
  void separatesAProbeThatRecordedNothingFromOneThatWasNeverOpened() {
    Probes.open("K.quiet.3");

    Map<String, Object> quiet = AgentBridge.drain("K.quiet.3", 0L, 100);
    assertEquals(Collections.emptyList(), quiet.get("events"));
    assertEquals(0L, ((Number) quiet.get("dropped")).longValue());
    assertEquals(Boolean.TRUE, quiet.get("active"));

    Map<String, Object> absent = AgentBridge.drain("K.never.3", 0L, 100);
    assertEquals(Boolean.FALSE, absent.get("active"));
    assertNotNull(absent.get("error"), "an unopened key must say so rather than look quiet");
  }

  /**
   * A closed probe reports its end state rather than reading as a typo, so a reader that polls once
   * more after a span ends learns the span finished.
   */
  @Test
  void reportsTheEndStateOfARecentlyClosedProbe() {
    Probes.open("K.done.1");
    record("K.done.1", 2);
    DiagAgent.closeProbe("K.done.1");

    Map<String, Object> read = AgentBridge.drain("K.done.1", 0L, 100);

    assertEquals(Boolean.FALSE, read.get("active"));
    assertEquals(Boolean.TRUE, read.get("closed"));
    assertEquals(
        2L,
        ((Number) read.get("writeSeq")).longValue(),
        "the final sequence tells a reader whether it saw everything");
  }

  @Test
  void refusesAProbeKeyTheInjectedCodeCouldNotCarry() {
    assertThrows(IllegalArgumentException.class, () -> AgentBridge.drain("~compiled/m", 0L, 10));
  }

  /**
   * The agent owns the deadline; the WAR owns the redefinition. This is the callback that joins
   * them, installed as a Proxy because the WAR cannot name an interface that lives on the system
   * class path.
   */
  @Test
  void installsARestorerTheAgentCanCallBackIntoTheWarWith() throws Exception {
    AgentBridge.installRestorer();

    assertNotNull(DiagAgent.RESTORER, "the agent has no way back without one");
    // A malformed key is rejected before anything touches Instrumentation,
    // which is what makes this assertable without attaching an agent.
    Exception thrown =
        assertThrows(Exception.class, () -> DiagAgent.RESTORER.restore("no-separator"));
    assertTrue(
        String.valueOf(thrown.getMessage()).contains("malformed lease key")
            || String.valueOf(thrown.getCause()).contains("malformed lease key"),
        () -> String.valueOf(thrown));
  }

  @Test
  void registersALeaseUnderTheKeyThePatchStashUses() {
    AgentBridge.lease("3f", "com.example.Target", 30);

    List<String> keys = new java.util.ArrayList<String>(DiagAgent.LEASES.keySet());
    assertEquals(Collections.singletonList("3f|com.example.Target"), keys);
    long deadline = DiagAgent.LEASES.get("3f|com.example.Target");
    assertTrue(
        deadline > System.currentTimeMillis() + 20_000L
            && deadline <= System.currentTimeMillis() + 30_000L,
        "the deadline is the requested 30s from now, not the default");
  }

  /**
   * The two states a patch register exists to tell apart: one a live span is holding open, and one
   * nothing will ever undo. Zero is what says the second, and it has to be reportable rather than
   * absent.
   */
  @Test
  void reportsWhenALeasedClassWillBeRestoredAndZeroForOneWithNoLease() {
    AgentBridge.lease("3f", "com.example.Leased", 30);

    long deadline = AgentBridge.leaseExpiresAt("3f", "com.example.Leased");
    assertTrue(deadline > System.currentTimeMillis(), "a live lease is in the future");
    assertEquals(DiagAgent.LEASES.get("3f|com.example.Leased").longValue(), deadline);
    assertEquals(0L, AgentBridge.leaseExpiresAt("3f", "com.example.Unleased"));
  }

  @Test
  void fallsBackToTheDefaultLeaseWhenTheCallerNamesNone() {
    AgentBridge.lease("3f", "com.example.Target", 0);

    long deadline = DiagAgent.LEASES.get("3f|com.example.Target");
    assertTrue(
        deadline > System.currentTimeMillis() + (AgentBridge.DEFAULT_LEASE_SECONDS - 10) * 1000L,
        "a lease with no length must still have one; an unbounded patch is the failure this"
            + " prevents");
  }
}
