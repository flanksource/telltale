package com.flanksource.telltale.diag.jmx;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

final class GcNotificationsTest {
  @Test
  void projectsHeapAndOldGenerationWithoutCountingNonHeapPools() {
    Map<String, Object> gc = new LinkedHashMap<>();
    gc.put("action", "end of minor GC");
    gc.put("pools", Arrays.asList(pool("Eden", true, 300, 10), pool("G1 Old Gen", true, 200, 220),
        pool("Code Cache", false, 800, 700)));
    GcNotifications.project(gc);
    assertEquals("minor", gc.get("actionKind"));
    assertEquals(500L, gc.get("heapBeforeBytes"));
    assertEquals(230L, gc.get("heapAfterBytes"));
    assertEquals(270L, gc.get("reclaimedBytes"));
    assertEquals("G1 Old Gen", gc.get("oldGenPool"));
    assertEquals(200L, gc.get("oldGenBeforeBytes"));
    assertEquals(220L, gc.get("oldGenAfterBytes"));
  }

  @Test
  void projectsNonGenerationalCollectorsWithoutInventingAnOldGeneration() {
    Map<String, Object> gc = new LinkedHashMap<>();
    gc.put("action", "end of concurrent GC");
    gc.put("pools", Collections.singletonList(pool("ZHeap", true, 500, 250)));
    GcNotifications.project(gc);
    assertEquals("other", gc.get("actionKind"));
    assertEquals(250L, gc.get("reclaimedBytes"));
    assertFalse(gc.containsKey("oldGenBeforeBytes"));
  }

  private static Map<String, Object> pool(String name, boolean heap, long before, long after) {
    Map<String, Object> pool = new LinkedHashMap<>();
    pool.put("name", name);
    pool.put("heap", heap);
    pool.put("usedBefore", before);
    pool.put("usedAfter", after);
    return pool;
  }
}
