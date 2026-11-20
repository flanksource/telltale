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
 * A filtered probe holds each root call on its own thread and publishes it only when the call
 * qualifies, so a capture of a hot method contains the calls someone asked about rather than the
 * first thousand that happened.
 */
final class ProbeFilterTest {

  private static final String KEY = "filter-5e6f7a8b";
  private static final String CLASS = "example.Worker";

  @BeforeEach
  @AfterEach
  void isolate() {
    DiagAgent.closeAllProbes();
  }

  @Test
  void keepsOnlyRootCallsAtLeastMinCostAndCountsTheRest() throws Exception {
    Probes.open(KEY, options("minCost", "40ms"));

    call("fast", 0L);
    call("slow", 80L);
    call("fast", 0L);

    assertEquals(Arrays.asList("enter", "exit"), Probes.kinds(KEY));
    assertEquals("slow", Probes.first(KEY, "enter").get("method"));
    assertTrue(((Number) Probes.first(KEY, "exit").get("durationNs")).longValue() >= 40_000_000L);
    assertEquals(2L, drained().get("filtered"));
  }

  @Test
  void exceptionsOnlyKeepsTheThrowingCallWithEverythingNestedInIt() {
    Probes.open(KEY, options("exceptionsOnly", Boolean.TRUE));

    long ok = DiagAgent.probeEnter(KEY, CLASS, "ok", "()V", 0, null);
    DiagAgent.probeExit(KEY, ok);
    long failing = DiagAgent.probeEnter(KEY, CLASS, "failing", "()V", 0, null);
    long nested = DiagAgent.probeEnter(KEY, CLASS, "nested", "()V", 12, null);
    DiagAgent.probeExit(KEY, nested);
    DiagAgent.probeThrow(KEY, failing, new IllegalStateException("boom"));

    assertEquals(Arrays.asList("enter", "enter", "exit", "throw"), Probes.kinds(KEY));
    assertEquals(1L, drained().get("filtered"));
  }

  @Test
  void matchesRenderedArgumentsByIndexPathAndOperator() {
    Map<String, Object> options = options("args", Boolean.TRUE);
    options.put(
        "match",
        Arrays.asList(
            predicate("arg", 0, null, "equals", "SubmitTask"),
            predicate("arg", 1, "name", "regex", "^acme-[0-9]+$")));
    Probes.open(KEY, options);

    enterExit(new Object[] {"SubmitTask", new Named("acme-17")});
    enterExit(new Object[] {"SubmitTask", new Named("other")});
    enterExit(new Object[] {"Poll", new Named("acme-17")});

    assertEquals(Arrays.asList("enter", "exit"), Probes.kinds(KEY));
    assertEquals("SubmitTask", Probes.argValues(Probes.first(KEY, "enter")).get(0));
    assertEquals(2L, drained().get("filtered"));
  }

  @Test
  void matchesTheRenderedReturnValueAndDropsACallThatThrewInstead() {
    Map<String, Object> options = options("return", Boolean.TRUE);
    options.put(
        "match", Collections.singletonList(predicate("return", 0, null, "contains", "sent")));
    Probes.open(KEY, options);

    returning("sent:queue");
    returning("skipped");
    long threw = DiagAgent.probeEnter(KEY, CLASS, "send", "()V", 0, null);
    DiagAgent.probeThrow(KEY, threw, new IllegalStateException("no return"));

    assertEquals(Arrays.asList("enter", "return", "exit"), Probes.kinds(KEY));
    assertEquals(2L, drained().get("filtered"));
  }

  /**
   * The per-thread buffer is bounded by the probe's event budget. A call that outgrows it keeps its
   * head and its closing events, and says how many it omitted.
   */
  @Test
  void keepsTheHeadAndTheCloseOfACallThatOutgrowsTheBuffer() {
    Map<String, Object> options = options("exceptionsOnly", Boolean.TRUE);
    options.put("events", Integer.valueOf(4));
    Probes.open(KEY, options);

    long root = DiagAgent.probeEnter(KEY, CLASS, "root", "()V", 0, null);
    for (int i = 0; i < 5; i++) {
      DiagAgent.probeExit(KEY, DiagAgent.probeEnter(KEY, CLASS, "child", "()V", i + 1, null));
    }
    DiagAgent.probeThrow(KEY, root, new IllegalStateException("late"));

    List<Map<String, Object>> events = Probes.events(KEY);
    assertEquals(
        Arrays.asList("enter", "enter", "exit", "enter", "throw"), Probes.kinds(KEY), "head + close");
    assertEquals(7, ((Number) events.get(4).get("omitted")).intValue());
    assertEquals(7L, drained().get("omitted"));
  }

  @Test
  void refusesFiltersItCannotEvaluateBeforeAnythingIsInstrumented() {
    List<String> errors = new ArrayList<String>();
    errors.add(refusal(options("minCost", "fast")));
    Map<String, Object> argsOff = new LinkedHashMap<String, Object>();
    argsOff.put("match", Collections.singletonList(predicate("arg", 0, null, "equals", "x")));
    errors.add(refusal(argsOff));
    Map<String, Object> badRegex = options("args", Boolean.TRUE);
    badRegex.put("match", Collections.singletonList(predicate("arg", 0, null, "regex", "(")));
    errors.add(refusal(badRegex));
    Map<String, Object> badOp = options("args", Boolean.TRUE);
    badOp.put("match", Collections.singletonList(predicate("arg", 0, null, "startsWith", "x")));
    errors.add(refusal(badOp));

    assertTrue(errors.get(0).contains("minCost"), errors.get(0));
    assertTrue(errors.get(1).contains("args"), errors.get(1));
    assertTrue(errors.get(2).contains("regex"), errors.get(2));
    assertTrue(errors.get(3).contains("startsWith"), errors.get(3));
  }

  @Test
  void parsesGoDurationsIncludingCompoundOnes() {
    assertEquals(1_500_000_000L, ProbeFilter.parseDuration("1.5s"));
    assertEquals(90_000_000_000L, ProbeFilter.parseDuration("1m30s"));
    assertEquals(250_000L, ProbeFilter.parseDuration("250us"));
    assertEquals(0L, ProbeFilter.parseDuration("0"));
  }

  private static String refusal(Map<String, Object> options) {
    Map<String, Object> opened = DiagAgent.openProbe(KEY, "test|" + KEY, options);
    assertEquals(Boolean.FALSE, opened.get("opened"), "open must refuse " + options);
    return String.valueOf(opened.get("error"));
  }

  private static void call(String method, long sleepMillis) throws InterruptedException {
    long frame = DiagAgent.probeEnter(KEY, CLASS, method, "()V", 0, null);
    if (sleepMillis > 0) {
      Thread.sleep(sleepMillis);
    }
    DiagAgent.probeExit(KEY, frame);
  }

  private static void enterExit(Object[] args) {
    DiagAgent.probeExit(KEY, DiagAgent.probeEnter(KEY, CLASS, "send", "()V", 0, args));
  }

  private static void returning(String value) {
    long frame = DiagAgent.probeEnter(KEY, CLASS, "send", "()Ljava/lang/String;", 0, null);
    DiagAgent.probeReturn(KEY, frame, value);
    DiagAgent.probeExit(KEY, frame);
  }

  private static Map<String, Object> drained() {
    return DiagAgent.drain(KEY, 0L, Integer.MAX_VALUE);
  }

  private static Map<String, Object> options(String name, Object value) {
    Map<String, Object> options = new LinkedHashMap<String, Object>();
    options.put(name, value);
    return options;
  }

  private static Map<String, Object> predicate(
      String on, int index, String path, String op, String value) {
    Map<String, Object> predicate = new LinkedHashMap<String, Object>();
    predicate.put("on", on);
    predicate.put("index", Integer.valueOf(index));
    if (path != null) {
      predicate.put("path", path);
    }
    predicate.put("op", op);
    predicate.put("value", value);
    return predicate;
  }

  /** A bean whose rendered form has one child, {@code name}. */
  static final class Named {
    final String name;

    Named(String name) {
      this.name = name;
    }
  }
}
