package com.flanksource.telltale.diag.jvm;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

final class HeapHistogramServiceTest {

  /** JDK 8 prints no module; JDK 9+ appends {@code (module@version)} or a loader description. */
  private static final String OUTPUT =
      String.join(
          "\n",
          " num     #instances         #bytes  class name (module)",
          "-------------------------------------------------------",
          "   1:          5000         480000  [B (java.base@17.0.2)",
          "   2:          9000         216000  java.lang.String (java.base@17.0.2)",
          "   3:            40        1048576  example.Cache",
          "   4:           300           7200  example.Order (app)",
          "Total         14340        1751776",
          "");

  @Test
  void parsesRowsSortedByBytesWithTheHeapTotals() {
    Map<String, Object> histogram = HeapHistogramService.parse(OUTPUT, 10, null, true);

    assertEquals(14340L, histogram.get("totalInstances"));
    assertEquals(1751776L, histogram.get("totalBytes"));
    List<Map<String, Object>> rows = rows(histogram);
    assertEquals(
        Arrays.asList("example.Cache", "[B", "java.lang.String", "example.Order"),
        column(rows, "className"));
    assertEquals(3, rows.get(0).get("rank"), "rank is the JDK's position in the full histogram");
    assertEquals("java.base@17.0.2", rows.get(1).get("loader"));
    assertFalse(rows.get(0).containsKey("loader"), "no loader reported, none invented");
  }

  @Test
  void filtersByGlobAndCutsToTopWhileReportingHowManyMatched() {
    Map<String, Object> histogram = HeapHistogramService.parse(OUTPUT, 1, "example.*", false);

    assertEquals(Arrays.asList("example.Cache"), column(rows(histogram), "className"));
    assertEquals(2, histogram.get("matched"));
    assertEquals(Boolean.FALSE, histogram.get("live"));
  }

  @Test
  void refusesOutputItCannotParseRatherThanReportingAnEmptyHeap() {
    IllegalStateException refused =
        assertThrows(
            IllegalStateException.class,
            () -> HeapHistogramService.parse("Permission denied\n", 10, null, false));
    assertTrue(refused.getMessage().contains("Permission denied"), refused.getMessage());
  }

  @Test
  void readsThisJvmsHeapThroughTheDiagnosticCommandMBean() throws Exception {
    Map<String, Object> params = new LinkedHashMap<String, Object>();
    params.put("top", 3);
    params.put("classPattern", "java.lang.*");
    Map<String, Object> histogram = new HeapHistogramService().histogram(params);

    List<Map<String, Object>> rows = rows(histogram);
    assertEquals(3, rows.size());
    long previous = Long.MAX_VALUE;
    for (Map<String, Object> row : rows) {
      assertTrue(String.valueOf(row.get("className")).startsWith("java.lang."), row.toString());
      long bytes = ((Number) row.get("bytes")).longValue();
      assertTrue(bytes <= previous, "sorted by bytes, largest first");
      previous = bytes;
    }
    assertTrue(((Number) histogram.get("totalBytes")).longValue() > 0L);
  }

  @Test
  void refusesANonPositiveTop() {
    Map<String, Object> params = new LinkedHashMap<String, Object>();
    params.put("top", 0);
    assertThrows(IllegalArgumentException.class, () -> new HeapHistogramService().histogram(params));
  }

  @SuppressWarnings("unchecked")
  private static List<Map<String, Object>> rows(Map<String, Object> histogram) {
    return (List<Map<String, Object>>) histogram.get("rows");
  }

  private static List<Object> column(List<Map<String, Object>> rows, String name) {
    List<Object> values = new java.util.ArrayList<Object>();
    for (Map<String, Object> row : rows) {
      values.add(row.get(name));
    }
    return values;
  }
}
