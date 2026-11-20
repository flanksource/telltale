package com.flanksource.telltale;

import static org.junit.jupiter.api.Assertions.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class SettingsTest {
  @Test void collectsSettingsAndHistogramWithoutMutationPermission() throws Exception {
    Diagnostics diagnostics = new Diagnostics(Collections.emptyMap());
    Map<?, ?> settings = (Map<?, ?>) diagnostics.call("jvm.settings", Collections.emptyMap(), false);
    assertEquals(System.getProperty("java.version"), ((Map<?, ?>) settings.get("properties")).get("java.version"));
    assertTrue(settings.containsKey("operatingSystem"));
    Map<?, ?> histogram = (Map<?, ?>) diagnostics.call("jvm.heap.histogram", Collections.emptyMap(), false);
    assertEquals(Boolean.FALSE, histogram.get("live"));
    assertTrue(((Number) histogram.get("totalInstances")).longValue() > 0);
    assertTrue(((List<?>) histogram.get("rows")).stream().anyMatch(row -> "java.lang.String".equals(((Map<?, ?>) row).get("className"))));
    assertTrue(((String) diagnostics.call("jvm.flags", Collections.emptyMap(), false)).contains("UseCompressedOops"));
  }
}
