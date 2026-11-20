package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.hotpatch.InstrumentationService;
import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import javax.jms.*;
import org.junit.Test;
import org.springframework.context.annotation.*;
import org.springframework.jms.annotation.EnableJms;
import org.springframework.jms.annotation.JmsListener;
import org.springframework.jms.config.DefaultJmsListenerContainerFactory;
import org.springframework.jms.core.JmsMessagingTemplate;
import org.springframework.jms.core.JmsTemplate;
import org.springframework.jms.listener.AbstractMessageListenerContainer;
import org.springframework.jms.listener.DefaultMessageListenerContainer;
import org.springframework.jms.listener.SessionAwareMessageListener;
import org.springframework.jms.listener.SimpleMessageListenerContainer;
import org.springframework.jms.support.converter.MessageConversionException;
import org.springframework.jms.support.converter.MessageConverter;
import org.springframework.messaging.handler.annotation.SendTo;

public class JmsSpringTest {
  @Test
  public void preservesRealBrokerRollbackAndRedeliveryAsSeparateProcessingAttempts() throws Exception {
    try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker()) {
      DefaultMessageListenerContainer container = new DefaultMessageListenerContainer();
      container.setConnectionFactory(broker.factory);
      container.setDestinationName("orders");
      container.setSessionTransacted(true);
      AtomicInteger attempts = new AtomicInteger();
      CountDownLatch accepted = new CountDownLatch(1);
      List<Throwable> failures = new CopyOnWriteArrayList<>();
      container.setErrorHandler(failures::add);
      container.setMessageListener((SessionAwareMessageListener<Message>) (message, session) -> {
        if (attempts.incrementAndGet() == 1) throw new java.lang.IllegalStateException("retry this order");
        accepted.countDown();
      });
      container.afterPropertiesSet();
      container.start();
      try {
        ObserverRuntime.install(InstrumentationService.instrumentation());
        try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "spring-redelivery", Collections.emptyMap())) {
          new JmsTemplate(broker.factory).convertAndSend("orders", "retry-order");
          assertTrue(accepted.await(5, TimeUnit.SECONDS));
          List<Map<String, Object>> rows = new ArrayList<>();
          long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
          while (count(rows, "process") < 2 && System.nanoTime() < deadline) {
            rows.addAll(capture.drain());
            Thread.sleep(10);
          }
          List<Map<String, Object>> processing = new ArrayList<>();
          for (Map<String, Object> row : rows) if ("process".equals(row.get("operation"))) processing.add(row);
          assertEquals(2, attempts.get());
          assertEquals(1, failures.size());
          assertEquals(2, processing.size());
          assertEquals("error", processing.get(0).get("outcome"));
          assertEquals(false, processing.get(0).get("redelivered"));
          assertEquals("success", processing.get(1).get("outcome"));
          assertEquals(true, processing.get(1).get("redelivered"));
          assertEquals(processing.get(0).get("messageId"), processing.get(1).get("messageId"));
        }
      } finally { container.stop(); container.destroy(); }
    }
  }

  @Test
  public void observesAnExistingAnnotatedListenerAndItsReplyInTheSameTransaction() throws Exception {
    try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker();
        AnnotationConfigApplicationContext context = new AnnotationConfigApplicationContext()) {
      context.registerBean("connectionFactory", ConnectionFactory.class, () -> broker.factory);
      context.register(ListenerConfiguration.class);
      context.refresh();
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "spring-annotated", Collections.emptyMap())) {
        JmsTemplate template = new JmsTemplate(broker.factory);
        template.setReceiveTimeout(5000);
        template.convertAndSend("orders", "order-123");
        assertEquals("accepted-order-123", template.receiveAndConvert("replies"));
        List<Map<String, Object>> rows = awaitProcessing(capture);
        assertEquals(1, count(rows, "process"));
        assertEquals(2, count(rows, "send"));
        Map<String, Object> process = rows.stream().filter(row -> "process".equals(row.get("operation"))).findFirst().get();
        assertEquals(true, process.get("transacted"));
        assertEquals("success", process.get("outcome"));
        assertTrue(((String) process.get("handler")).endsWith("OrderListener.accept"));
        assertTrue(rows.stream().anyMatch(row -> "send".equals(row.get("operation")) && "replies".equals(row.get("destination"))));
      }
    }
  }

  @Test
  public void capturesTemplateWrappersAndConversionFailuresOnce() throws Exception {
    try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker()) {
      JmsTemplate template = new JmsTemplate(broker.factory);
      template.setReceiveTimeout(1000);
      JmsMessagingTemplate messaging = new JmsMessagingTemplate(template);
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "spring-template", Collections.emptyMap())) {
        messaging.convertAndSend("orders", "spring-order");
        assertEquals("spring-order", template.receiveAndConvert("orders"));
        List<Map<String, Object>> rows = capture.drain();
        assertEquals(1, count(rows, "send"));
        assertEquals(1, count(rows, "receive"));
        template.setMessageConverter(new MessageConverter() {
          public Message toMessage(Object value, Session session) { throw new MessageConversionException("invalid order"); }
          public Object fromMessage(Message message) { throw new MessageConversionException("invalid reply"); }
        });
        try { template.convertAndSend("orders", "unconvertible"); fail("conversion must throw"); }
        catch (MessageConversionException expected) { assertEquals("invalid order", expected.getMessage()); }
        rows = capture.drain();
        assertTrue(rows.isEmpty());
      }
    }
  }

  @Test
  public void observesAlreadyRunningDefaultAndSimpleContainersIncludingSwallowedErrors() throws Exception {
    for (boolean simple : new boolean[]{false, true}) {
      try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker()) {
        CountDownLatch received = new CountDownLatch(1);
        AbstractMessageListenerContainer container = simple ? new SimpleMessageListenerContainer() : new DefaultMessageListenerContainer();
        container.setConnectionFactory(broker.factory);
        container.setDestinationName("orders");
        container.setErrorHandler(error -> received.countDown());
        container.setMessageListener((SessionAwareMessageListener<Message>) (message, session) -> {
          throw new java.lang.IllegalStateException("handler rejected order");
        });
        container.afterPropertiesSet();
        container.start();
        try {
          ObserverRuntime.install(InstrumentationService.instrumentation());
          try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "spring-container-" + simple, Collections.emptyMap())) {
            new JmsTemplate(broker.factory).convertAndSend("orders", "rejected-order");
            assertTrue(received.await(5, TimeUnit.SECONDS));
            List<Map<String, Object>> rows = awaitProcessing(capture);
            assertEquals(1, count(rows, "process"));
            Map<String, Object> process = rows.stream().filter(row -> "process".equals(row.get("operation"))).findFirst().get();
            assertEquals("error", process.get("outcome"));
            assertEquals("handler rejected order", process.get("error"));
            assertEquals("spring-jms", process.get("framework"));
            assertEquals(false, process.get("transacted"));
          }
        } finally { container.stop(); container.destroy(); }
      }
    }
  }

  private static long count(List<Map<String, Object>> rows, String operation) {
    return rows.stream().filter(row -> operation.equals(row.get("operation"))).count();
  }

  private static List<Map<String, Object>> awaitProcessing(ObserverSubscription capture) throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
    while (System.nanoTime() < deadline) {
      rows.addAll(capture.drain());
      if (count(rows, "process") > 0) return rows;
      Thread.sleep(10);
    }
    throw new AssertionError("No Spring processing record before deadline: " + rows);
  }

  @Configuration
  @EnableJms
  public static class ListenerConfiguration {
    @Bean
    public DefaultJmsListenerContainerFactory jmsListenerContainerFactory(ConnectionFactory factory) {
      DefaultJmsListenerContainerFactory listeners = new DefaultJmsListenerContainerFactory();
      listeners.setConnectionFactory(factory);
      listeners.setSessionTransacted(true);
      listeners.setConcurrency("1");
      return listeners;
    }

    @Bean
    public OrderListener orders() { return new OrderListener(); }
  }

  public static class OrderListener {
    @JmsListener(destination = "orders")
    @SendTo("replies")
    public String accept(String body) { return "accepted-" + body; }
  }
}
