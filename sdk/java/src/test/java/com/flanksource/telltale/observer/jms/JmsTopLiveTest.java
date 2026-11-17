package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;
import com.flanksource.telltale.diag.hotpatch.InstrumentationService;
import com.flanksource.telltale.observer.*;
import java.util.*;
import java.util.concurrent.*;
import javax.jms.*;
import org.junit.Test;

public class JmsTopLiveTest {
  @Test public void mapsBusyAndIdleHandlersWithoutPublishingReceivePolls() throws Exception {
    try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker(); Connection sender = broker.factory.createConnection()) {
      MessageConsumer consumer = broker.session.createConsumer(broker.destination);
      CountDownLatch entered = new CountDownLatch(1), release = new CountDownLatch(1);
      consumer.setMessageListener(message -> {
        entered.countDown();
        try { if (!release.await(5,TimeUnit.SECONDS)) throw new AssertionError("listener was not released"); }
        catch (InterruptedException error) { throw new AssertionError(error); }
      });
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription events = ObserverRuntime.subscribe("jms","jms-live-events",Collections.emptyMap());
           ObserverSubscription top = ObserverRuntime.subscribe("jms_top","jms-live-top",Collections.emptyMap())) {
        Session session = sender.createSession(false,Session.AUTO_ACKNOWLEDGE);
        MessageProducer producer = session.createProducer(broker.destination);
        try {
          producer.send(session.createTextMessage("order"));
          assertTrue(entered.await(5,TimeUnit.SECONDS));
          List<Map<String,Object>> busy = top.snapshot();
          assertEquals(1,busy.size());
          Map<?,?> attributes = (Map<?,?>) busy.get(0).get("attributes");
          assertEquals("orders",attributes.get("destination"));
          assertEquals("busy",attributes.get("state"));
          assertEquals(1,attributes.get("activeCount"));
          release.countDown();
          List<Map<String,Object>> messages = new ArrayList<>();
          long deadline = System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
          while (messages.stream().noneMatch(row -> "process".equals(row.get("operation"))) && System.nanoTime()<deadline) {
            messages.addAll(events.drain()); Thread.sleep(10);
          }
          assertEquals(1,messages.stream().filter(row -> "process".equals(row.get("operation"))).count());
          assertTrue(messages.stream().noneMatch(row -> "timeout".equals(row.get("outcome"))));
          List<Map<String,Object>> idle = top.snapshot();
          assertEquals(busy.get(0).get("operationId"),idle.get(0).get("operationId"));
          assertEquals("idle",((Map<?,?>)idle.get(0).get("attributes")).get("state"));
          events.close();
          assertEquals(1,top.snapshot().size());
        } finally { release.countDown(); }
      }
    }
  }
}
