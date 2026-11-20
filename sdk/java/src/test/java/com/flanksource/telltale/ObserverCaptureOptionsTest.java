package com.flanksource.telltale;

import static org.junit.Assert.*;
import java.util.Collections;
import org.junit.Test;

public class ObserverCaptureOptionsTest {
  @Test public void refusesFractionalPollIntervalsBeforeAttaching() throws Exception { reject("pollIntervalMs"); }
  @Test public void refusesFractionalRowLimitsBeforeAttaching() throws Exception { reject("limit"); }
  private static void reject(String key) throws Exception {
    try { new ObserverCapture("http_top", Collections.singletonMap(key, 3.5), "fractional-" + key, Collections.emptyMap()); fail("fractional limit accepted"); }
    catch (IllegalArgumentException expected) { assertTrue(expected.getMessage().contains(key)); }
  }
}
