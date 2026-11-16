package com.flanksource.telltale.diag.jvm;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

final class JvmMetricsServiceTest {

  @Test
  void snapshotsStandardJvmRuntimeAndMemoryMetrics() throws Exception {
    Map<String, Object> snapshot = new JvmMetricsService().snapshot();

    assertNotNull(snapshot.get("capturedAt"));
    assertNotNull(snapshot.get("runtime"));
    @SuppressWarnings("unchecked")
    Map<String, List<Map<String, Object>>> memory =
        (Map<String, List<Map<String, Object>>>) snapshot.get("memory");
    assertFalse(memory.get("heap").isEmpty());
    assertNotNull(snapshot.get("garbageCollectors"));
    assertNotNull(snapshot.get("series"));
  }
}
