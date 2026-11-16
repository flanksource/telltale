package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import com.flanksource.telltale.observer.bootstrap.ObserverBridge;
import java.util.function.BiFunction;
import org.junit.Test;

public class ObserverBridgeTest {
  @Test public void preservesApplicationValuesWhenErrorReportingThrowsTheSameFailure() {
    BiFunction<String, Object[], Object> before = ObserverBridge.handler;
    IllegalStateException failure = new IllegalStateException("observer callback failed");
    ObserverBridge.handler = (action, arguments) -> { throw failure; };
    try {
      assertNull(ObserverBridge.enter("fixture", "fixture.Client", "request", "()V", null, new Object[0]));
      assertEquals("original", ObserverBridge.exit(new Object(), "original", null));
      assertEquals(0, failure.getSuppressed().length);
    } finally { ObserverBridge.handler = before; }
  }
}
