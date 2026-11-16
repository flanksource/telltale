package com.flanksource.telltale.diag.hotpatch;

import static org.junit.Assert.*;

import java.util.Collections;
import org.junit.Test;

public class ClassOperationsTest {
  @Test
  public void readsExactNumericCursorsFromOperationBodies() {
    assertEquals(
        9007199254740993L,
        ClassOperations.cursor(new JsonBody(Collections.singletonMap("since", 9007199254740993L))));
    assertEquals(0L, ClassOperations.cursor(new JsonBody(Collections.emptyMap())));
    for (Object value : new Object[] {1.5, -1L, "17"}) {
      try {
        ClassOperations.cursor(new JsonBody(Collections.singletonMap("since", value)));
        fail("accepted invalid cursor " + value);
      } catch (IllegalArgumentException expected) {
        assertTrue(expected.getMessage().contains("since"));
      }
    }
  }
}
