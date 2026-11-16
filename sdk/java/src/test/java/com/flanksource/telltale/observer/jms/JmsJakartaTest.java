package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.hotpatch.InstrumentationService;
import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import jakarta.jms.*;
import java.io.File;
import java.net.URL;
import java.net.URLClassLoader;
import java.util.*;
import java.util.concurrent.*;
import org.junit.Test;

public class JmsJakartaTest {
  @Test
  public void observesSpringSixAndSevenWithTheJakartaBrokerClient() throws Exception {
    for (String version : new String[]{"spring6", "spring7"}) {
      List<URL> jars = new ArrayList<>();
      jars.add(new File(required("telltale.jms.jakartaClient")).toURI().toURL());
      for (String path : required("telltale.jms." + version).split(java.util.regex.Pattern.quote(File.pathSeparator)))
        jars.add(new File(path).toURI().toURL());
      try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker();
          URLClassLoader loader = new JakartaClient(jars.toArray(new URL[0]))) {
        Object factory = loader.loadClass("org.apache.activemq.artemis.jms.client.ActiveMQConnectionFactory")
            .getConstructor(String.class).newInstance("vm://0");
        try {
          Object template = loader.loadClass("org.springframework.jms.core.JmsTemplate")
              .getConstructor(ConnectionFactory.class).newInstance(factory);
          template.getClass().getMethod("setReceiveTimeout", long.class).invoke(template, 1000L);
          for (String type : new String[]{"DefaultMessageListenerContainer", "SimpleMessageListenerContainer"}) {
            modernContainer(loader, factory, template, type, version);
          }
          if (version.equals("spring7")) modernClient(loader, (ConnectionFactory) factory);
        } finally { ((AutoCloseable) factory).close(); }
      }
    }
  }

  private static void modernClient(ClassLoader loader, ConnectionFactory factory) throws Exception {
    Class<?> clientType = loader.loadClass("org.springframework.jms.core.JmsClient");
    Class<?> operations = loader.loadClass("org.springframework.jms.core.JmsClient$OperationSpec");
    Object client = clientType.getMethod("create", ConnectionFactory.class).invoke(null, factory);
    Object queue = clientType.getMethod("destination", String.class).invoke(client, "orders");
    operations.getMethod("withReceiveTimeout", long.class).invoke(queue, 1000L);
    ObserverRuntime.install(InstrumentationService.instrumentation());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "spring7-client", Collections.emptyMap())) {
      operations.getMethod("send", Object.class).invoke(queue, "fluent-order");
      assertEquals(Optional.of("fluent-order"), operations.getMethod("receive", Class.class).invoke(queue, String.class));
      List<Map<String, Object>> rows = capture.drain();
      assertEquals(1, rows.stream().filter(row -> "send".equals(row.get("operation"))).count());
      assertEquals(1, rows.stream().filter(row -> "receive".equals(row.get("operation"))).count());
      assertEquals(Optional.empty(), operations.getMethod("receive", Class.class).invoke(queue, String.class));
      rows = capture.drain();
      assertEquals(1, rows.size());
      assertEquals("timeout", rows.get(0).get("outcome"));
    }
  }

  private static void modernContainer(ClassLoader loader, Object factory, Object template, String type, String version) throws Exception {
    Object container = loader.loadClass("org.springframework.jms.listener." + type).getConstructor().newInstance();
    Class<?> listener = loader.loadClass("org.springframework.jms.listener.SessionAwareMessageListener");
    CountDownLatch processed = new CountDownLatch(1);
    Object callback = java.lang.reflect.Proxy.newProxyInstance(loader, new Class<?>[]{listener}, (proxy, method, args) -> {
      if (method.getName().equals("onMessage")) processed.countDown();
      if (method.getName().equals("hashCode")) return System.identityHashCode(proxy);
      if (method.getName().equals("equals")) return proxy == args[0];
      if (method.getName().equals("toString")) return "JMS test listener";
      return null;
    });
    container.getClass().getMethod("setConnectionFactory", ConnectionFactory.class).invoke(container, factory);
    container.getClass().getMethod("setDestinationName", String.class).invoke(container, "orders");
    container.getClass().getMethod("setMessageListener", Object.class).invoke(container, callback);
    container.getClass().getMethod("afterPropertiesSet").invoke(container);
    container.getClass().getMethod("start").invoke(container);
    try {
      ObserverRuntime.install(InstrumentationService.instrumentation());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", version + "-" + type, Collections.emptyMap())) {
        template.getClass().getMethod("convertAndSend", String.class, Object.class).invoke(template, "orders", "modern-order");
        assertTrue(processed.await(5, TimeUnit.SECONDS));
        List<Map<String, Object>> rows = new ArrayList<>();
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (System.nanoTime() < deadline && rows.stream().noneMatch(row -> "process".equals(row.get("operation")))) {
          rows.addAll(capture.drain());
          Thread.sleep(10);
        }
        assertEquals(1, rows.stream().filter(row -> "send".equals(row.get("operation"))).count());
        assertEquals(1, rows.stream().filter(row -> "process".equals(row.get("operation"))).count());
        assertTrue(rows.stream().allMatch(row -> !"error".equals(row.get("outcome"))));
        assertSame(callback, container.getClass().getMethod("getMessageListener").invoke(container));
      }
    } finally {
      container.getClass().getMethod("stop").invoke(container);
      container.getClass().getMethod("destroy").invoke(container);
    }
  }

  private static String required(String property) {
    String value = System.getProperty(property);
    assertNotNull(property + " fixture path must be configured", value);
    return value;
  }

  @Test
  public void observesPreconfiguredJakartaSimplifiedAsyncSendsWithoutChangingCallbacks() throws Exception {
    String client = System.getProperty("telltale.jms.jakartaClient");
    assertNotNull("Jakarta JMS client fixture path must be configured", client);
    try (JmsObserverTest.Broker broker = new JmsObserverTest.Broker();
        URLClassLoader loader = new JakartaClient(new File(client).toURI().toURL())) {
      Object factory = loader.loadClass("org.apache.activemq.artemis.jms.client.ActiveMQConnectionFactory")
          .getConstructor(String.class).newInstance("vm://0");
      try (JMSContext context = ((ConnectionFactory) factory).createContext()) {
        jakarta.jms.Queue destination = context.createQueue("orders");
        JMSProducer producer = context.createProducer();
        JMSConsumer consumer = context.createConsumer(destination);
        CountDownLatch completed = new CountDownLatch(1);
        CompletionListener callback = new CompletionListener() {
          public void onCompletion(Message message) { completed.countDown(); }
          public void onException(Message message, Exception error) { throw new AssertionError(error); }
        };
        producer.setAsync(callback);
        ObserverRuntime.install(InstrumentationService.instrumentation());
        try (ObserverSubscription capture = ObserverRuntime.subscribe("jms", "jakarta-existing", Collections.emptyMap())) {
          producer.send(destination, "jakarta-order");
          assertTrue(completed.await(5, TimeUnit.SECONDS));
          assertEquals("jakarta-order", consumer.receiveBody(String.class, 1000));
          assertSame(callback, producer.getAsync());
          List<Map<String, Object>> rows = capture.drain();
          assertEquals(1, rows.stream().filter(row -> "send".equals(row.get("operation"))).count());
          assertEquals(1, rows.stream().filter(row -> "receive".equals(row.get("operation"))).count());
          assertTrue(rows.stream().allMatch(row -> "success".equals(row.get("outcome"))));
          assertTrue(rows.stream().filter(row -> "send".equals(row.get("operation"))).allMatch(row -> row.get("messageId") != null));
        }
      } finally { ((AutoCloseable) factory).close(); }
    }
  }

  private static final class JakartaClient extends URLClassLoader {
    JakartaClient(URL... jars) { super(jars, JmsJakartaTest.class.getClassLoader()); }

    @Override
    protected synchronized Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!name.startsWith("org.apache.activemq.artemis.jms.client.")
          && !name.startsWith("org.springframework.") && !name.startsWith("io.micrometer.")) return super.loadClass(name, resolve);
      Class<?> type = findLoadedClass(name);
      if (type == null) type = findClass(name);
      if (resolve) resolveClass(type);
      return type;
    }
  }
}
