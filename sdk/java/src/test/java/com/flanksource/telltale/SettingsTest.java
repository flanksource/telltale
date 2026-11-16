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
    String histogram = (String) diagnostics.call("jvm.heap.histogram", Collections.emptyMap(), false);
    assertTrue(histogram.contains("java.lang.String"));
    assertTrue(histogram.contains("Total"));
  }
}
