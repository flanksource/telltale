package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.AllocFixture;
import com.flanksource.telltale.diag.fixture.Probes;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * {@code alloc} through real rewritten bytecode: the instrumented method's own frame and each
 * call-site frame the {@code calls} mode wraps both carry the bytes allocated between their entry
 * and exit.
 */
final class InstrumentAllocTest {

  private static final String CLASS_NAME = AllocFixture.class.getName();
  private static final int ONE_MEGABYTE = 1 << 20;

  private final InstrumentService service = new InstrumentService();

  @BeforeEach
  @AfterEach
  void clearProbes() {
    Probes.closeAll();
  }

  @Test
  void measuresTheInstrumentedMethodAndEachCallSiteItMade() throws Exception {
    String key = "AllocFixture.outer.m1";
    InstrumentService.Result result =
        service.instrument(
            new InstrumentService.Request(
                    CLASS_NAME,
                    LiveClass.bytecodeOf(AllocFixture.class),
                    AllocFixture.class.getClassLoader(),
                    MethodSelector.parse("outer"),
                    key)
                .withCalls(true));
    Probes.open(key, Probes.options("alloc", Boolean.TRUE));
    Class<?> instrumented = LiveClass.define(CLASS_NAME, result.getBytecode());

    Object fixture = instrumented.getConstructor().newInstance();
    assertEquals(
        ONE_MEGABYTE, instrumented.getMethod("outer", int.class).invoke(fixture, ONE_MEGABYTE));

    Map<Long, String> methodOfFrame = new HashMap<Long, String>();
    Map<String, Long> allocOf = new HashMap<String, Long>();
    for (Map<String, Object> event : Probes.events(key)) {
      long frame = ((Number) event.get("frame")).longValue();
      if ("enter".equals(event.get("kind"))) {
        methodOfFrame.put(frame, String.valueOf(event.get("method")));
      } else if ("exit".equals(event.get("kind")) && event.get("allocBytes") instanceof Number) {
        allocOf.put(methodOfFrame.get(frame), ((Number) event.get("allocBytes")).longValue());
      }
    }
    assertTrue(
        allocOf.containsKey("fill") && allocOf.get("fill") >= ONE_MEGABYTE,
        () -> "the call site of fill() carries its array: " + allocOf);
    assertTrue(
        allocOf.containsKey("outer") && allocOf.get("outer") >= ONE_MEGABYTE,
        () -> "outer's own frame includes what it called: " + allocOf);
  }
}
