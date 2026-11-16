package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Before;
import org.junit.Test;

public class ObserverLeaseTest {
  @Before public void attach() throws Exception { ObserverRuntime.install(ByteBuddyAgent.install()); }

  @Test public void expiresAnAbandonedCaptureAndReleasesItsHooks() throws Exception {
    String kind = "lease-expiry";
    ObserverRuntime.register(new EmptyAdapter(kind));
    ObserverSubscription capture = ObserverRuntime.subscribe(kind, kind, Collections.singletonMap("leaseMillis", 100));
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
    while (ObserverRuntime.armed(kind) && System.nanoTime() < deadline) Thread.sleep(20);
    assertFalse("lease expiry must detach its subscription", ObserverRuntime.armed(kind));
    assertTrue(capture.expired());
    try { capture.check(); fail("expired captures must report their failure"); }
    catch (IllegalStateException expected) { assertTrue(expected.getCause().getMessage().contains("lease expired")); }
    capture.close();
  }

  @Test public void renewalKeepsTheSameCaptureAliveUntilItIsClosed() throws Exception {
    String kind = "lease-renewal";
    ObserverRuntime.register(new EmptyAdapter(kind));
    try (ObserverSubscription capture = ObserverRuntime.subscribe(kind, kind, Collections.singletonMap("leaseMillis", 250))) {
      for (int i = 0; i < 6; i++) { Thread.sleep(60); capture.renew(); }
      assertTrue(ObserverRuntime.armed(kind));
      assertFalse(capture.expired());
    }
    assertFalse(ObserverRuntime.armed(kind));
  }

  @Test public void stoppingOneCaptureRetainsItsSiblingAndItsOwnTerminalRows() throws Exception {
    String kind = "lease-siblings";
    ObserverRuntime.register(new EmptyAdapter(kind) {
      public List<Map<String, Object>> terminalRows(String stopped, Map<String, Object> params) {
        return Collections.singletonList(Collections.singletonMap("label", params.get("label")));
      }
    });
    ObserverSubscription first = ObserverRuntime.subscribe(kind, "first-lease", Collections.singletonMap("label", "first"));
    try (ObserverSubscription second = ObserverRuntime.subscribe(kind, "second-lease", Collections.singletonMap("label", "second"))) {
      first.close();
      assertTrue(ObserverRuntime.armed(kind));
      assertEquals(Collections.singletonList(Collections.singletonMap("label", "first")), first.drain());
      assertTrue(second.drain().isEmpty());
      ObserverRuntime.emit(kind, Collections.singletonMap("active", true));
      assertTrue(first.drain().isEmpty());
      assertEquals(Collections.singletonList(Collections.singletonMap("active", true)), second.drain());
    }
    assertFalse(ObserverRuntime.armed(kind));
  }

  private static class EmptyAdapter implements ObserverAdapter {
    final String kind;
    EmptyAdapter(String kind) { this.kind = kind; }
    public String id() { return kind; }
    public Set<String> kinds() { return Collections.singleton(kind); }
    public boolean matchesType(String name, Set<String> hierarchy) { return false; }
    public boolean matchesMethod(String name, String descriptor) { return false; }
    public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return null; }
    public Object onExit(Object token, Object result, Throwable error) { return result; }
  }
}
