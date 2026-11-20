package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;
import java.lang.ref.WeakReference;
import java.util.*;
import org.junit.Test;

public class JmsHandlersTest {
  @Test public void closingOneConsumerPreservesOtherConsumersAndTheirActiveCount() {
    JmsHandlers handlers = new JmsHandlers();
    Object first = new Object(), second = new Object();
    JmsOperation firstDelivery = delivery(first), secondDelivery = delivery(second);
    handlers.enter(firstDelivery);
    handlers.enter(secondDelivery);
    String id = (String) handlers.snapshot().get(0).get("operationId");
    assertEquals(2, attributes(handlers).get("activeCount"));
    handlers.remove(first);
    assertEquals(1, handlers.snapshot().size());
    assertEquals(id, handlers.snapshot().get(0).get("operationId"));
    assertEquals(1, attributes(handlers).get("activeCount"));
    handlers.exit(secondDelivery);
    assertEquals("idle", attributes(handlers).get("state"));
    handlers.remove(second);
    assertTrue(handlers.snapshot().isEmpty());
  }

  private static JmsOperation delivery(Object owner) {
    JmsOperation operation = new JmsOperation("process");
    operation.put("destination", "orders");
    operation.put("handler", "OrderListener.onMessage");
    operation.handlerOwner = new WeakReference<>(owner);
    return operation;
  }

  private static Map<?,?> attributes(JmsHandlers handlers) {
    return (Map<?,?>) handlers.snapshot().get(0).get("attributes");
  }
}
