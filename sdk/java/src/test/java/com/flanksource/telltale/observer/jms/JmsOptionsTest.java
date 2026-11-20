package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;
import java.util.*;
import org.junit.Test;

public class JmsOptionsTest {
  @Test
  public void projectsEachSubscriptionsExactDestinationAndUtf8BodyBudget() {
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("destinations", Collections.singletonList("orders"));
    params.put("body", true);
    params.put("maxBodyBytes", 3);
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("destination", "orders");
    row.put("body", "éé");
    Map<String, Object> projected = JmsOptions.project(params, new LinkedHashMap<>(row));
    assertEquals("é", projected.get("body"));
    assertEquals(true, projected.get("bodyTruncated"));
    assertEquals("éé", row.get("body"));
    params.put("body", false);
    assertFalse(JmsOptions.project(params, new LinkedHashMap<>(row)).containsKey("body"));
    row.put("destination", "other-queue");
    assertNull(JmsOptions.project(params, row));
  }

  @Test
  public void refusesInvalidJavaCaptureConfigurationAtItsBoundary() {
    for (Map<String, Object> params : Arrays.asList(
        Collections.<String, Object>singletonMap("direction", "sideways"),
        Collections.<String, Object>singletonMap("body", "true"),
        Collections.<String, Object>singletonMap("destinations", Collections.singletonList(" ")),
        Collections.<String, Object>singletonMap("maxBodyBytes", 5),
        Collections.<String, Object>singletonMap("capacity", -1))) {
      try { JmsOptions.validate(params); fail("Invalid configuration accepted: " + params); }
      catch (IllegalArgumentException expected) { assertNotNull(expected.getMessage()); }
    }
  }
}
