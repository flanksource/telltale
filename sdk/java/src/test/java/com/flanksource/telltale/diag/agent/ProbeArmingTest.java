package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.Probes;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * What a class is running has to be a function of the probes still open on it.
 *
 * <p>A class is redefined whole, so arming a second span into a class a first span already armed
 * replaces the first span's rewritten methods. The WAR therefore rebuilds the class from every open
 * arming rather than amending it, and this is where it reads what those armings are. Before the
 * agent kept them, the preservation plan's three spans on ActivityDal ended with only the
 * last-armed one recording anything, and the other two reported a clean capture of nothing.
 */
final class ProbeArmingTest {

  private static final String OWNER = "3f|example.application.pas.dal.ActivityDal";
  private static final String OTHER_OWNER = "3f|example.application.pas.bll.ActivityBll";

  @BeforeEach
  @AfterEach
  void isolate() {
    DiagAgent.closeAllProbes();
  }

  @Test
  void keepsOneArmingPerSpanRecordingOnAClass() {
    open("probe-find-1111", OWNER, "findNextPendingActivity(*)");
    open("probe-build-2222", OWNER, "buildActivityDcl(*)");

    assertEquals(
        Arrays.asList("findNextPendingActivity(*)", "buildActivityDcl(*)"),
        selectorsOn(OWNER),
        "a second span arming the class must not replace the first span's arming");
  }

  @Test
  void ordersArmingsAsTheyWereArmedRatherThanAsTheKeysHash() {
    List<String> armed = new ArrayList<>();
    // Up to the JVM's probe-key cap, which is what a single step can hold.
    for (int i = 8; i > 0; i--) {
      String selector = "method" + i + "(*)";
      open("probe-order-" + i + "000", OWNER, selector);
      armed.add(selector);
    }

    // Rebuilding a class has to produce the same bytes every time: two spans
    // arming ONE method nest one inside the other, and which is inner cannot
    // depend on the iteration order of the probe map.
    assertEquals(armed, selectorsOn(OWNER));
  }

  @Test
  void reportsOnlyTheArmingsOfTheClassAskedAbout() {
    open("probe-dal-3333", OWNER, "findNextPendingActivity(*)");
    open("probe-bll-4444", OTHER_OWNER, "process(*)");

    assertEquals(Arrays.asList("process(*)"), selectorsOn(OTHER_OWNER));
  }

  @Test
  void carriesTheProbeKeyAndCaptureFlagsBackToTheRewriter() {
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("method", "buildFindNextPendingActivityDclBy*Query");
    options.put("return", Boolean.TRUE);
    Probes.open("probe-return-5555", OWNER, options);

    Map<String, Object> arming = DiagAgent.probeArmingsOwnedBy(OWNER).get(0);
    assertEquals("probe-return-5555", arming.get("key"));
    assertEquals(OWNER, arming.get("owner"));
    assertEquals(Boolean.TRUE, arming.get("return"));
  }

  /**
   * One span arming several methods joins its own key, and each arm is its own rewrite of the class
   * — so joining must add an arming, not replace one.
   */
  @Test
  void keepsEveryArmingOfASpanThatJoinsItsOwnKey() {
    Map<String, Object> first = new LinkedHashMap<>();
    first.put("method", "findNextPendingActivity(*)");
    Probes.open("probe-join-6666", OWNER, first);

    Map<String, Object> second = new LinkedHashMap<>();
    second.put("method", "buildActivityDcl(*)");
    second.put("join", Boolean.TRUE);
    Probes.open("probe-join-6666", OWNER, second);

    assertEquals(
        Arrays.asList("findNextPendingActivity(*)", "buildActivityDcl(*)"), selectorsOn(OWNER));
  }

  /** A closed probe's arming goes with it: the class no longer records for it. */
  @Test
  void forgetsAnArmingWhenItsProbeCloses() {
    open("probe-closed-7777", OWNER, "findNextPendingActivity(*)");
    open("probe-live-8888", OWNER, "buildActivityDcl(*)");

    DiagAgent.closeProbe("probe-closed-7777");

    assertEquals(Arrays.asList("buildActivityDcl(*)"), selectorsOn(OWNER));
  }

  @Test
  void reportsNothingForAClassWithNoOpenProbe() {
    assertTrue(DiagAgent.probeArmingsOwnedBy(OWNER).isEmpty());
    assertTrue(DiagAgent.probeArmingsOwnedBy(null).isEmpty());
  }

  private static void open(String key, String owner, String selector) {
    Probes.open(key, owner, Collections.<String, Object>singletonMap("method", selector));
  }

  private static List<String> selectorsOn(String owner) {
    List<String> selectors = new ArrayList<>();
    for (Map<String, Object> arming : DiagAgent.probeArmingsOwnedBy(owner)) {
      selectors.add(String.valueOf(arming.get("method")));
    }
    return selectors;
  }
}
