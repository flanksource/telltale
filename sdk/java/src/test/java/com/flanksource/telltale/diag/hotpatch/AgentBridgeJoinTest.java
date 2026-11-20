package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.agent.DiagAgent;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * One span arming several targets under ONE probe key, as the WAR sees it.
 *
 * <p>The ring outlives any one of its classes: a class is released from it while the others keep it
 * open. That is where the two things this covers come from — the arming a released class leaves
 * behind in a ring that is still open, and a reader telling which of the span's classes the ring
 * still records for.
 */
final class AgentBridgeJoinTest {

  private static final String SPAN = "Uip.saveActivity.join1";
  private static final String LATER = "Uip.other.join2";

  @BeforeEach
  @AfterEach
  void isolate() {
    DiagAgent.closeAllProbes();
  }

  /**
   * Releasing class A from a ring class B keeps open must take A's arming with it. The agent keeps
   * every arming a ring was opened with, so without the filter the next rebuild of A re-applies the
   * released span's selector and A starts recording into a ring that no longer claims it.
   */
  @Test
  void rebuildsAReleasedClassWithoutTheSharedRingsStaleArming() {
    DiagAgent.openProbe(SPAN, "3f|com.example.A", armedFor("a(*)", false));
    DiagAgent.openProbe(SPAN, "3f|com.example.B", armedFor("b(*)", true));
    AgentBridge.releaseProbe(SPAN, "3f", "com.example.A");

    DiagAgent.openProbe(LATER, "3f|com.example.A", armedFor("c(*)", false));

    assertEquals(
        Collections.singletonList("c(*)"),
        selectors(AgentBridge.probeArmingsOwnedBy("3f", "com.example.A")),
        "only the arming of a ring still holding A may be rebuilt into it");
    assertEquals(
        Collections.singletonList("b(*)"),
        selectors(AgentBridge.probeArmingsOwnedBy("3f", "com.example.B")));
  }

  /**
   * Two selectors on ONE class under one key are two armings of one owner, so the rebuild carries
   * both, and one release lets go of the class.
   */
  @Test
  void appliesTwoSelectorsOnOneClassAndRestoresItOnOneRelease() {
    DiagAgent.openProbe(SPAN, "3f|com.example.Uip", armedFor("saveActivity(*)", false));
    DiagAgent.openProbe(SPAN, "3f|com.example.Uip", armedFor("checkWarning(*)", true));

    assertEquals(
        Arrays.asList("saveActivity(*)", "checkWarning(*)"),
        selectors(AgentBridge.probeArmingsOwnedBy("3f", "com.example.Uip")),
        "both selectors are rebuilt into the class");

    Map<String, Object> released = AgentBridge.releaseProbe(SPAN, "3f", "com.example.Uip");

    assertEquals(Boolean.TRUE, released.get("closed"), "the class was the ring's only owner");
    assertTrue(
        AgentBridge.probeKeysOwnedBy("3f", "com.example.Uip").isEmpty(),
        "nothing holds the class any more, so the restore puts its bytes back");
  }

  /**
   * A read names the classes the ring still records for, so a span whose class another surface
   * restored can say that target was revoked while the ring itself stays open for the others.
   */
  @Test
  void reportsTheOwnersAProbeStillRecordsForOnEachRead() {
    DiagAgent.openProbe(SPAN, "3f|com.example.A", armedFor("a(*)", false));
    DiagAgent.openProbe(SPAN, "3f|com.example.B", armedFor("b(*)", true));

    assertEquals(
        new HashSet<>(Arrays.asList("3f|com.example.A", "3f|com.example.B")),
        owners(AgentBridge.drain(SPAN, 0L, 10)));

    AgentBridge.closeProbesOwnedBy("3f", "com.example.A");

    assertEquals(
        Collections.singleton("3f|com.example.B"),
        owners(AgentBridge.drain(SPAN, 0L, 10)),
        "a blunt restore of A drops A from the ring and leaves B recording");
  }

  @Test
  void reportsNoOwnersOnAClosedProbe() {
    DiagAgent.openProbe(SPAN, "3f|com.example.A", armedFor("a(*)", false));
    DiagAgent.closeProbe(SPAN);

    Map<String, Object> read = AgentBridge.drain(SPAN, 0L, 10);

    assertEquals(Boolean.FALSE, read.get("active"));
    assertFalse(read.containsKey("owners"), "a closed ring records for nobody");
  }

  private static Map<String, Object> armedFor(String method, boolean join) {
    Map<String, Object> options = new LinkedHashMap<String, Object>();
    options.put("method", method);
    if (join) {
      options.put("join", Boolean.TRUE);
    }
    return options;
  }

  private static List<String> selectors(List<Map<String, Object>> armings) {
    List<String> out = new ArrayList<String>();
    for (Map<String, Object> arming : armings) {
      out.add(String.valueOf(arming.get("method")));
    }
    return out;
  }

  private static java.util.Set<String> owners(Map<String, Object> read) {
    Object owners = read.get("owners");
    assertTrue(owners instanceof List, () -> "an open probe's read names its owners: " + read);
    java.util.Set<String> out = new HashSet<String>();
    for (Object owner : (List<?>) owners) {
      out.add(String.valueOf(owner));
    }
    return out;
  }
}
