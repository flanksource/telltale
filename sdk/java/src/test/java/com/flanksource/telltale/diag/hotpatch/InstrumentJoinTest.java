package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.ProbeFixture;
import com.flanksource.telltale.diag.fixture.Probes;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Two selectors on one class, armed under ONE key, rebuilt the way {@code
 * ClassesServlet.armEveryOpenProbe} rebuilds a class: each arming applied in turn to the bytes the
 * previous one produced.
 *
 * <p>Run rather than inspected, for the reason {@link InstrumentServiceTest} gives: the JVM
 * verifies the twice-rewritten bytes as it defines them.
 */
final class InstrumentJoinTest {

  private static final String CLASS_NAME = ProbeFixture.class.getName();
  private static final String KEY = "ProbeFixture.outer.join3";
  private static final String OWNER = "3f|" + CLASS_NAME;

  private final InstrumentService service = new InstrumentService();

  @BeforeEach
  void clearProbes() {
    Probes.closeAll();
  }

  @Test
  void appliesBothSelectorsAndNestsTheInnerTargetUnderTheOuter() throws Exception {
    Probes.open(KEY, OWNER, arming("outer()", false));
    Probes.open(KEY, OWNER, arming("inner()", true));
    InstrumentService.Result outer =
        service.instrument(request(LiveClass.bytecodeOf(ProbeFixture.class), "outer()"));
    InstrumentService.Result both = service.instrument(request(outer.getBytecode(), "inner()"));

    Class<?> instrumented = LiveClass.define(CLASS_NAME, both.getBytecode());
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    assertEquals(
        "outer:inner",
        instrumented.getMethod("outer").invoke(fixture),
        "two wraps on one class must not change what it computes");
    Map<String, Object> outerEnter = enterOf("outer");
    Map<String, Object> innerEnter = enterOf("inner");
    assertEquals(
        outerEnter.get("frame"),
        innerEnter.get("parent"),
        "the inner target nests under the outer one in the span's one tree");
    assertTrue(
        ((Number) innerEnter.get("seq")).longValue() > ((Number) outerEnter.get("seq")).longValue(),
        "one ring orders both targets on one sequence");
  }

  private InstrumentService.Request request(byte[] bytecode, String selector) {
    return new InstrumentService.Request(
        CLASS_NAME,
        bytecode,
        ProbeFixture.class.getClassLoader(),
        MethodSelector.parse(selector),
        KEY);
  }

  private static Map<String, Object> arming(String selector, boolean join) {
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("method", selector);
    if (join) {
      options.put("join", Boolean.TRUE);
    }
    return options;
  }

  private static Map<String, Object> enterOf(String method) {
    List<Map<String, Object>> events = Probes.events(KEY);
    for (Map<String, Object> event : events) {
      if ("enter".equals(event.get("kind")) && method.equals(event.get("method"))) {
        return event;
      }
    }
    throw new AssertionError("no entry of " + method + " in " + events);
  }
}
