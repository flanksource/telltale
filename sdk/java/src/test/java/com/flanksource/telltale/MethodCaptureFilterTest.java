package com.flanksource.telltale;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.RetransformFixture;
import com.flanksource.telltale.diag.hotpatch.ClassIndex;
import com.flanksource.telltale.diag.hotpatch.ClassOperations;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** A filtered jvm_trace capture reports what its filter withheld, alongside what it recorded. */
final class MethodCaptureFilterTest {

  @Test
  void reportsTheCallsTheFilterWithheld() throws Exception {
    Map<String, Object> match = new LinkedHashMap<>();
    match.put("on", "arg");
    match.put("index", 0);
    match.put("op", "contains");
    match.put("value", "Submit");
    MethodCapture capture = capture(Collections.singletonList(match));
    try {
      RetransformFixture fixture = new RetransformFixture();
      fixture.send("Cycle.SubmitTask");
      fixture.send("Cycle.Poll");
      fixture.send("Cycle.Poll");

      String events = new String(capture.dump(), StandardCharsets.UTF_8);
      assertTrue(events.contains("Cycle.SubmitTask"), events);
      assertTrue(!events.contains("Cycle.Poll"), events);
      Map<String, Object> report = capture.report();
      assertEquals(2L, report.get("filtered"));
      assertEquals(0L, report.get("omitted"));
    } finally {
      capture.close();
    }
  }

  @Test
  void anUnfilteredCaptureHasNoReport() throws Exception {
    MethodCapture capture = capture(null);
    try {
      assertNull(capture.report());
    } finally {
      capture.close();
    }
  }

  private static MethodCapture capture(Object match) throws Exception {
    Map<String, Object> target = new LinkedHashMap<>();
    target.put("class", RetransformFixture.class.getName());
    target.put("method", "send");
    target.put("loader", ClassIndex.hash(RetransformFixture.class.getClassLoader()));
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("targets", Collections.singletonList(target));
    params.put("args", Boolean.TRUE);
    if (match != null) params.put("match", match);
    return new MethodCapture(new ClassOperations(), "jvm_trace", params, "MethodCaptureFilterTest.k1");
  }
}
