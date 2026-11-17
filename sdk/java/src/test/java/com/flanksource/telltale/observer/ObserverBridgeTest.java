package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import com.flanksource.telltale.observer.bootstrap.ObserverBridge;
import java.util.function.BiFunction;
import org.junit.Test;

public class ObserverBridgeTest {
  @Test public void finishesAnInvocationInTheRuntimeThatStartedItAfterTheHandlerChanges() {
    BiFunction<String, Object[], Object> before = ObserverBridge.handler;
    Object started = new Object();
    java.util.List<String> calls = new java.util.ArrayList<>();
    ObserverBridge.handler = (action, arguments) -> {
      calls.add(action);
      if (action.equals("enter")) return started;
      assertSame(started, arguments[0]);
      return "completed";
    };
    try {
      Object token = ObserverBridge.enter("fixture", "fixture.Client", "request", "()V", null, new Object[0]);
      ObserverBridge.handler = (action, arguments) -> { throw new AssertionError("old callback reached the replacement runtime"); };
      assertEquals("completed", ObserverBridge.exit(token, "original", null));
      assertEquals(java.util.Arrays.asList("enter", "exit"), calls);
    } finally { ObserverBridge.handler = before; }
  }

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
