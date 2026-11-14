package com.flanksource.telltale.diag.agent;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.Probes;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * One trace over several methods: every target is armed under ONE probe key, the second and later
 * joining the ring the first opened.
 *
 * <p>What makes that worth doing is nesting. Parentage comes from the agent's per-thread frame
 * stack, which every key shares, and order from the ring's one sequence — so a call from one target
 * into another lands under its caller in one tree, which two separate keys can never give.
 */
final class ProbeJoinNestingTest {

  private static final String KEY = "Outer.a.join1";
  private static final String OUTER = "3f|com.example.Outer";
  private static final String INNER = "3f|com.example.Inner";

  @BeforeEach
  @AfterEach
  void isolate() {
    DiagAgent.closeAllProbes();
  }

  @Test
  void nestsACallFromOneJoinedTargetIntoAnotherUnderItsCaller() {
    Probes.open(KEY, OUTER, arming("a()", false));
    Probes.open(KEY, INNER, arming("b()", true));

    long outer = DiagAgent.probeEnter(KEY, "com.example.Outer", "a", "()V", 0, null);
    long inner = DiagAgent.probeEnter(KEY, "com.example.Inner", "b", "()V", 0, null);
    DiagAgent.probeExit(KEY, inner);
    DiagAgent.probeExit(KEY, outer);

    List<Map<String, Object>> events = Probes.events(KEY);
    assertEquals(
        Arrays.asList("enter", "enter", "exit", "exit"),
        Probes.kinds(KEY),
        "both targets record into the one ring the span reads");
    assertEquals("b", events.get(1).get("method"));
    assertEquals(
        outer,
        ((Number) events.get(1).get("parent")).longValue(),
        "the inner target's frame points at the outer target's frame");
    assertEquals(1, ((Number) events.get(1).get("depth")).intValue());
    assertTrue(
        strictlyIncreasing(sequences(events)),
        () -> "one ring orders both targets on one sequence: " + sequences(events));
  }

  private static Map<String, Object> arming(String selector, boolean join) {
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("method", selector);
    if (join) {
      options.put("join", Boolean.TRUE);
    }
    return options;
  }

  private static List<Long> sequences(List<Map<String, Object>> events) {
    List<Long> seqs = new ArrayList<>();
    for (Map<String, Object> event : events) {
      seqs.add(((Number) event.get("seq")).longValue());
    }
    return seqs;
  }

  private static boolean strictlyIncreasing(List<Long> values) {
    for (int i = 1; i < values.size(); i++) {
      if (values.get(i) <= values.get(i - 1)) {
        return false;
      }
    }
    return true;
  }
}
