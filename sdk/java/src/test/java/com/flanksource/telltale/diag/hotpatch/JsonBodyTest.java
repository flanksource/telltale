package com.flanksource.telltale.diag.hotpatch;

import static org.junit.Assert.*;

import java.util.Collections;
import org.junit.Test;

public class JsonBodyTest {
  @Test
  public void refusesFractionalAndOverflowingIntegers() {
    for (Number value : new Number[] {1.5, 4294967296L, Double.NaN}) {
      try {
        new JsonBody(Collections.singletonMap("seconds", value)).signedInteger("seconds");
        fail("accepted " + value);
      } catch (IllegalArgumentException expected) {
        assertTrue(expected.getMessage().contains("integer"));
      }
    }
  }

  @Test
  public void refusesWrongTypesForOptionalFields() {
    try {
      new JsonBody(Collections.singletonMap("loader", 12)).string("loader");
      fail("accepted numeric loader");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage().contains("loader"));
    }
    try {
      new JsonBody(Collections.singletonMap("probe", "wrong")).optionalObject("probe");
      fail("accepted string probe");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage().contains("probe"));
    }
  }
}
