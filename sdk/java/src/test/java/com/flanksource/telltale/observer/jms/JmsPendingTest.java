package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;
import java.lang.ref.WeakReference;
import java.lang.reflect.Proxy;
import java.net.URL;
import java.net.URLClassLoader;
import java.util.*;
import java.util.concurrent.TimeUnit;
import javax.jms.Message;
import org.junit.Test;

public class JmsPendingTest {
  @Test
  public void boundsPendingOperationsByIdentityAndFailsInsteadOfEvicting() {
    JmsPending pending = new JmsPending(1);
    Object first = new String("same-value");
    Object second = new String("same-value");
    JmsOperation operation = new JmsOperation("send");
    operation.message(first);
    assertTrue(pending.put(first, operation));
    assertTrue(pending.put(first, operation));
    assertFalse(pending.put(second, new JmsOperation("send")));
    assertNull(pending.remove(second));
    assertSame(operation, pending.remove(first));
    assertTrue(pending.drain().isEmpty());
  }

  @Test
  public void releasesMessagesAndApplicationClassloadersWhileTheObserverRemainsInstalled() throws Exception {
    JmsPending pending = new JmsPending(10);
    JmsActive active = new JmsActive(10);
    Tracked tracked = pendingApplicationMessage(pending, active);
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
    while (System.nanoTime() < deadline && (tracked.message.get() != null || tracked.loader.get() != null)) {
      System.gc();
      System.runFinalization();
      Thread.sleep(10);
    }
    assertNull("Pending observer state retained the JMS message", tracked.message.get());
    assertNull("Pending observer state retained its application loader", tracked.loader.get());
    List<JmsOperation> collected = pending.collected();
    assertEquals(1, collected.size());
    assertNull(collected.get(0).message());
    assertTrue(pending.drain().isEmpty());
    assertEquals(1, active.terminalRows().size());
    active.clear();
  }

  private static Tracked pendingApplicationMessage(JmsPending pending, JmsActive active) throws Exception {
    try (URLClassLoader loader = new URLClassLoader(new URL[0], Message.class.getClassLoader())) {
      Object message = Proxy.newProxyInstance(loader, new Class<?>[]{Message.class}, (proxy, method, args) -> null);
      JmsOperation operation = new JmsOperation("send");
      operation.message(message);
      assertTrue(pending.put(message, operation));
      assertTrue(active.add(operation));
      return new Tracked(new WeakReference<>(message), new WeakReference<ClassLoader>(loader));
    }
  }

  private static final class Tracked {
    final WeakReference<Object> message;
    final WeakReference<ClassLoader> loader;
    Tracked(WeakReference<Object> message, WeakReference<ClassLoader> loader) { this.message = message; this.loader = loader; }
  }
}
