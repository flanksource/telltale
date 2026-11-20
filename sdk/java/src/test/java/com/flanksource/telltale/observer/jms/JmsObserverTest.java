package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.hotpatch.InstrumentationService;
import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.util.*;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import javax.jms.*;
import org.apache.activemq.artemis.api.core.TransportConfiguration;
import org.apache.activemq.artemis.core.config.impl.ConfigurationImpl;
import org.apache.activemq.artemis.core.remoting.impl.invm.InVMAcceptorFactory;
import org.apache.activemq.artemis.jms.client.ActiveMQConnectionFactory;
import org.apache.activemq.artemis.core.server.embedded.EmbeddedActiveMQ;
import org.junit.Test;

public class JmsObserverTest {
  @Test
  public void keepsAnInlineListenersReplySeparateFromItsEnclosingProducerSend() throws Exception {
    try (Broker broker = new Broker()) {
      List<Map<String, Object>> rows = new ArrayList<>();
      JmsObserver observer = new JmsObserver(rows::add, () -> 0);
      TextMessage request = broker.session.createTextMessage("inline-order");
      Object sending = observer.onEnter("javax.jms.MessageProducer", "send", "(Ljavax/jms/Message;)V", null, new Object[]{request});
      Object processing = observer.onEnter("javax.jms.MessageListener", "onMessage", "(Ljavax/jms/Message;)V", null, new Object[]{request});
      Object replying = observer.onEnter("org.springframework.jms.core.JmsTemplate", "convertAndSend", "(Ljava/lang/String;Ljava/lang/Object;)V", null, new Object[]{"replies", "reply"});
      Exception rejected = new org.springframework.jms.support.converter.MessageConversionException("reply conversion rejected");
      observer.onExit(replying, null, rejected);
      observer.onExit(processing, null, rejected);
      observer.onExit(sending, null, null);
      assertEquals(2, rows.size());
      assertEquals("error", rows.get(0).get("outcome"));
      assertEquals("process", rows.get(0).get("operation"));
      assertEquals("success", rows.get(1).get("outcome"));
    }
  }

  @Test
  public void closingOneCaptureDoesNotEndAnotherCapturesInFlightListener() throws Exception {
    try (Broker broker = new Broker(); Connection sender = broker.factory.createConnection()) {
      MessageConsumer consumer = broker.session.createConsumer(broker.destination);
      CountDownLatch entered = new CountDownLatch(1);
      CountDownLatch release = new CountDownLatch(1);
      consumer.setMessageListener(message -> {
        entered.countDown();
        try { if (!release.await(5, TimeUnit.SECONDS)) throw new AssertionError("Listener was not released"); }
        catch (InterruptedException error) { Thread.currentThread().interrupt(); throw new AssertionError(error); }
      });
      Session session = sender.createSession(false, Session.AUTO_ACKNOWLEDGE);
      MessageProducer producer = session.createProducer(broker.destination);
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription first = ObserverRuntime.subscribe("jms", "overlap-first", Collections.emptyMap());
          ObserverSubscription second = ObserverRuntime.subscribe("jms", "overlap-second", Collections.emptyMap())) {
        try {
          producer.send(session.createTextMessage("overlapping-order"));
          assertTrue(entered.await(5, TimeUnit.SECONDS));
          first.close();
          List<Map<String, Object>> initial = first.drain();
          assertEquals(1, initial.stream().filter(row -> "process".equals(row.get("operation")) && "incomplete".equals(row.get("outcome"))).count());
          release.countDown();
          List<Map<String, Object>> remaining = new ArrayList<>();
          long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
          while (remaining.stream().noneMatch(row -> "process".equals(row.get("operation"))) && System.nanoTime() < deadline) {
            remaining.addAll(second.drain());
            Thread.sleep(10);
          }
          assertEquals(1, remaining.stream().filter(row -> "process".equals(row.get("operation"))).count());
          assertTrue(remaining.stream().filter(row -> "process".equals(row.get("operation"))).allMatch(row -> "success".equals(row.get("outcome"))));
          assertTrue(first.drain().isEmpty());
        } finally { release.countDown(); }
      }
    }
  }

  @Test
  public void observesAPrecreatedMessageListenerLambdaWithoutReplacingItsConsumer() throws Exception {
    try (Broker broker = new Broker(); Connection sender = broker.factory.createConnection()) {
      MessageConsumer consumer = broker.session.createConsumer(broker.destination);
      CountDownLatch delivered = new CountDownLatch(1);
      AtomicReference<String> payload = new AtomicReference<>();
      MessageListener listener = message -> {
        try { payload.set(((TextMessage) message).getText()); }
        catch (JMSException error) { throw new AssertionError(error); }
        delivered.countDown();
      };
      consumer.setMessageListener(listener);
      Session session = sender.createSession(false, Session.AUTO_ACKNOWLEDGE);
      MessageProducer producer = session.createProducer(broker.destination);
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "existing-listener-lambda", Collections.emptyMap())) {
        producer.send(session.createTextMessage("lambda-order"));
        assertTrue(delivered.await(5, TimeUnit.SECONDS));
        List<Map<String, Object>> rows = new ArrayList<>();
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (rows.stream().noneMatch(row -> "process".equals(row.get("operation"))) && System.nanoTime() < deadline) {
          rows.addAll(capture.drain());
          Thread.sleep(10);
        }
        assertEquals("lambda-order", payload.get());
        assertSame(listener, consumer.getMessageListener());
        assertEquals(1, rows.stream().filter(row -> "process".equals(row.get("operation"))).count());
        assertTrue(rows.stream().filter(row -> "process".equals(row.get("operation"))).allMatch(row -> "orders".equals(row.get("destination"))));
      }
    }
  }

  @Test
  public void retransformsAnExistingBrokerClientAndPrecreatedCallbackWithoutRestart() throws Exception {
    try (Broker broker = new Broker()) {
      MessageProducer producer = broker.session.createProducer(broker.destination);
      MessageConsumer consumer = broker.session.createConsumer(broker.destination);
      CountDownLatch delivered = new CountDownLatch(1);
      CompletionListener callback = new CompletionListener() {
        public void onCompletion(Message message) { delivered.countDown(); }
        public void onException(Message message, Exception error) { throw new AssertionError(error); }
      };
      TextMessage message = broker.session.createTextMessage("attached-order");
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "jms-existing-client", Collections.emptyMap())) {
        producer.send(message, callback);
        assertTrue(delivered.await(5, TimeUnit.SECONDS));
        Message received = consumer.receive(1000);
        assertNotNull(received);
        List<Map<String, Object>> rows = capture.drain();
        assertEquals(1, rows.stream().filter(row -> row.get("operation").equals("send")).count());
        assertEquals(1, rows.stream().filter(row -> row.get("operation").equals("receive")).count());
        assertEquals("attached-order", ((TextMessage) received).getText());
      }
    }
  }

  @Test
  public void capturesExistingProducerAndConsumerWithoutChangingTheMessage() throws Exception {
    try (Broker broker = new Broker()) {
      MessageProducer producer = broker.session.createProducer(broker.destination);
      MessageConsumer consumer = broker.session.createConsumer(broker.destination);
      TextMessage message = broker.session.createTextMessage("order-123");
      List<Map<String, Object>> rows = new ArrayList<>();
      JmsObserver observer = new JmsObserver(rows::add, () -> 5);
      Object send = observer.onEnter(producer.getClass().getName(), "send", "(Ljavax/jms/Message;)V", producer, new Object[]{message});
      producer.send(message);
      observer.onExit(send, null, null);
      Object receive = observer.onEnter(consumer.getClass().getName(), "receive", "(J)Ljavax/jms/Message;", consumer, new Object[]{1000L});
      Message received = consumer.receive(1000);
      assertSame(received, observer.onExit(receive, received, null));
      assertEquals(2, rows.size());
      assertEquals("outgoing", rows.get(0).get("direction"));
      assertEquals("send", rows.get(0).get("operation"));
      assertEquals("orders", rows.get(1).get("destination"));
      assertEquals(message.getJMSMessageID(), rows.get(1).get("messageId"));
      assertEquals("order", rows.get(1).get("body"));
      assertEquals(true, rows.get(1).get("bodyTruncated"));
      assertEquals("order-123", ((TextMessage) received).getText());
    }
  }

  @Test
  public void waitsForAsynchronousCompletionAndDeduplicatesCallbackDispatch() throws Exception {
    try (Broker broker = new Broker()) {
      MessageProducer producer = broker.session.createProducer(broker.destination);
      TextMessage message = broker.session.createTextMessage("async-order");
      List<Map<String, Object>> rows = new CopyOnWriteArrayList<>();
      JmsObserver observer = new JmsObserver(rows::add, () -> 0);
      CountDownLatch completed = new CountDownLatch(1);
      CompletionListener listener = new CompletionListener() {
        public void onCompletion(Message delivered) {
          Object dispatch = observer.onEnter("javax.jms.CompletionListener", "onCompletion", "(Ljavax/jms/Message;)V", this, new Object[]{delivered});
          Object nested = observer.onEnter(getClass().getName(), "onCompletion", "(Ljavax/jms/Message;)V", this, new Object[]{delivered});
          observer.onExit(nested, null, null);
          observer.onExit(dispatch, null, null);
          completed.countDown();
        }
        public void onException(Message delivered, Exception error) { throw new AssertionError(error); }
      };
      Object token = observer.onEnter(producer.getClass().getName(), "send", "(Ljavax/jms/Message;Ljavax/jms/CompletionListener;)V", producer, new Object[]{message, listener});
      observer.onExit(token, null, null);
      assertTrue(rows.isEmpty());
      producer.send(message, listener);
      assertTrue(completed.await(5, TimeUnit.SECONDS));
      assertEquals(1, rows.size());
      assertEquals("success", rows.get(0).get("outcome"));
      assertFalse(rows.get(0).containsKey("body"));
    }
  }

  @Test
  public void excludesEmptyPollsAndReceiveFailuresWithoutMessages() {
    List<Map<String, Object>> rows = new ArrayList<>();
    JmsObserver observer = new JmsObserver(rows::add, () -> 0);
    Object token = observer.onEnter("javax.jms.MessageConsumer", "receive", "()Ljavax/jms/Message;", null, new Object[0]);
    observer.onExit(token, null, null);
    assertTrue(rows.isEmpty());
    token = observer.onEnter("javax.jms.MessageConsumer", "receive", "()Ljavax/jms/Message;", null, new Object[0]);
    Exception failure = new java.lang.IllegalStateException("broker unavailable");
    observer.onExit(token, null, failure);
    assertTrue(rows.isEmpty());
  }

  @Test
  public void reportsObjectBodiesWithoutDeserializingAndPreservesRedeliveryAttempts() throws Exception {
    try (Broker broker = new Broker()) {
      List<Map<String, Object>> rows = new ArrayList<>();
      JmsObserver observer = new JmsObserver(rows::add, () -> 100);
      ObjectMessage message = broker.session.createObjectMessage("opaque-order");
      for (int attempt = 0; attempt < 2; attempt++) {
        message.setJMSRedelivered(attempt > 0);
        Object dispatch = observer.onEnter("javax.jms.MessageListener", "onMessage", "(Ljavax/jms/Message;)V", null, new Object[]{message});
        Object nested = observer.onEnter("javax.jms.MessageListener", "onMessage", "(Ljavax/jms/Message;)V", null, new Object[]{message});
        observer.onExit(nested, null, null);
        observer.onExit(dispatch, null, null);
      }
      assertEquals(2, rows.size());
      assertEquals(false, rows.get(0).get("redelivered"));
      assertEquals(true, rows.get(1).get("redelivered"));
      assertTrue(((String) rows.get(0).get("bodyUnsupported")).contains("Non-text"));
      assertFalse(rows.get(0).containsKey("body"));
    }
  }

  @Test
  public void distinguishesANullConvertedBodyFromAReceiveTimeout() throws Exception {
    try (Broker broker = new Broker()) {
      List<Map<String, Object>> rows = new ArrayList<>();
      JmsObserver observer = new JmsObserver(rows::add, () -> 0);
      TextMessage message = broker.session.createTextMessage();
      Object converted = observer.onEnter("org.springframework.jms.core.JmsTemplate", "receiveAndConvert", "()Ljava/lang/Object;", null, new Object[0]);
      Object raw = observer.onEnter("javax.jms.MessageConsumer", "receive", "()Ljavax/jms/Message;", null, new Object[0]);
      observer.onExit(raw, message, null);
      observer.onExit(converted, null, null);
      assertEquals(1, rows.size());
      assertEquals("success", rows.get(0).get("outcome"));
    }
  }

  @Test
  public void failsTheObserverWhenThePendingSendLimitIsExceeded() throws Exception {
    try (Broker broker = new Broker()) {
      List<Message> retained = new ArrayList<>();
      List<Map<String, Object>> rows = new ArrayList<>();
      JmsObserver observer = new JmsObserver(rows::add, () -> 0);
      CompletionListener listener = new CompletionListener() {
        public void onCompletion(Message message) {}
        public void onException(Message message, Exception failure) {}
      };
      try {
        for (int index = 0; index <= 8192; index++) {
          Message message = broker.session.createTextMessage("pending-order");
          retained.add(message);
          Object call = observer.onEnter("javax.jms.MessageProducer", "send", "(Ljavax/jms/Message;Ljavax/jms/CompletionListener;)V", null, new Object[]{message, listener});
          observer.onExit(call, null, null);
        }
        fail("Pending overflow must fail observation");
      } catch (java.lang.IllegalStateException expected) {
        assertTrue(expected.getCause().getMessage().contains("8192 exceeded"));
        assertEquals(8193, retained.size());
        assertTrue(rows.isEmpty());
      } finally {
        List<Map<String, Object>> unfinished = observer.terminalRows("jms", Collections.emptyMap());
        assertEquals(8192, unfinished.size());
        assertTrue(unfinished.stream().allMatch(row -> "incomplete".equals(row.get("outcome"))));
        observer.onDisarm("jms");
      }
      assertTrue(rows.isEmpty());
    }
  }

  @Test
  public void preservesUtf8BoundariesAndDoesNotUseDestinationNamesAsBodies() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    JmsObserver observer = new JmsObserver(rows::add, () -> 3);
    Object token = observer.onEnter("org.springframework.jms.core.JmsTemplate", "convertAndSend", "(Ljava/lang/String;Ljava/lang/Object;)V", null, new Object[]{"orders", "payload"});
    observer.onExit(token, null, new IllegalArgumentException("conversion rejected"));
    assertTrue(rows.isEmpty());
    try (Broker broker = new Broker()) {
      TextMessage message = broker.session.createTextMessage("éé");
      token = observer.onEnter("javax.jms.MessageListener", "onMessage", "(Ljavax/jms/Message;)V", null, new Object[]{message});
      observer.onExit(token, null, null);
      assertEquals("é", rows.get(0).get("body"));
      assertEquals(true, rows.get(0).get("bodyTruncated"));
    }
  }

  static final class Broker implements AutoCloseable {
    final EmbeddedActiveMQ server;
    final ActiveMQConnectionFactory factory;
    final Connection connection;
    final Session session;
    final Destination destination;

    Broker() throws Exception {
      server = new EmbeddedActiveMQ();
      server.setConfiguration(new ConfigurationImpl().setPersistenceEnabled(false).setSecurityEnabled(false).addAcceptorConfiguration(new TransportConfiguration(InVMAcceptorFactory.class.getName())));
      server.start();
      factory = new ActiveMQConnectionFactory("vm://0");
      connection = factory.createConnection();
      session = connection.createSession(false, Session.AUTO_ACKNOWLEDGE);
      destination = session.createQueue("orders");
      connection.start();
    }

    public void close() throws Exception {
      connection.close();
      factory.close();
      server.stop();
    }
  }
}
