package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import observerfixture.ExistingClient;
import org.junit.Before;
import org.junit.Test;

public class ObserverSafetyTest {
  @Before public void attach() throws Exception { ObserverRuntime.install(ByteBuddyAgent.install()); }

  @Test public void rejectsAnIncompatibleReplacementWithoutChangingTheApplicationResult() throws Exception {
    String kind = "bad-replacement";
    ObserverRuntime.register(new Adapter(kind) {
      public Object onExit(Object token, Object result, Throwable error) { return 12; }
    });
    try (ObserverSubscription capture = ObserverRuntime.subscribe(kind, kind, Collections.emptyMap())) {
      assertEquals("original:value", new ExistingClient().request("value"));
      try { capture.check(); fail("invalid replacement must fail its capture"); }
      catch (IllegalStateException expected) { assertTrue(expected.getCause().getMessage().contains("incompatible")); }
    }
  }

  @Test public void restoresArgumentsAndIsolatesAnObserverFailureFromOtherCaptures() throws Exception {
    String bad = "bad-argument", good = "good-argument";
    ObserverRuntime.register(new Adapter(bad) {
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) {
        args[0] = 42; return "bad";
      }
    });
    ObserverRuntime.register(new Adapter(good) {
      public Object onExit(Object token, Object result, Throwable error) {
        ObserverRuntime.emit(good, Collections.singletonMap("result", result)); return result;
      }
    });
    try (ObserverSubscription failed = ObserverRuntime.subscribe(bad, bad, Collections.emptyMap());
        ObserverSubscription healthy = ObserverRuntime.subscribe(good, good, Collections.emptyMap())) {
      assertEquals("original:value", new ExistingClient().request("value"));
      assertEquals(Collections.singletonMap("result", "original:value"), healthy.drain().get(0));
      try { failed.check(); fail("invalid argument must fail its capture"); }
      catch (IllegalStateException expected) { assertTrue(expected.getCause().getMessage().contains("argument")); }
    }
  }

  @Test public void deliversTerminalRowsBeforeDetachingTheFinalSubscriber() throws Exception {
    String kind = "terminal";
    ObserverRuntime.register(new Adapter(kind) {
      public List<Map<String, Object>> terminalRows(String stopped, Map<String, Object> params) {
        assertFalse(ObserverRuntime.armed(kind));
        return Collections.singletonList(Collections.singletonMap("terminal", true));
      }
    });
    ObserverSubscription capture = ObserverRuntime.subscribe(kind, kind, Collections.emptyMap());
    capture.close();
    assertEquals(Collections.singletonList(Collections.singletonMap("terminal", true)), capture.drain());
    assertFalse(ObserverRuntime.armed(kind));
  }

  @Test public void reportsBoundedQueueLossWithoutInventingRecords() throws Exception {
    String kind = "capacity";
    ObserverRuntime.register(new Adapter(kind));
    try (ObserverSubscription capture = ObserverRuntime.subscribe(kind, kind, Collections.singletonMap("capacity", 1))) {
      ObserverRuntime.emit(kind, Collections.singletonMap("index", 1));
      ObserverRuntime.emit(kind, Collections.singletonMap("index", 2));
      assertEquals(1, capture.dropped());
      assertEquals(Collections.singletonList(Collections.singletonMap("index", 1)), capture.drain());
    }
  }

  @Test public void doesNotDeliverAnOperationFromAClosedCaptureToItsSuccessor() throws Exception {
    String kind = "start-boundary";
    ObserverRuntime.register(new Adapter(kind));
    Map<String, Object> old = new LinkedHashMap<>();
    try (ObserverSubscription before = ObserverRuntime.subscribe(kind, "before", Collections.emptyMap())) {
      old.put("startedAt", java.time.Instant.now().toString());
    }
    try (ObserverSubscription after = ObserverRuntime.subscribe(kind, "after", Collections.emptyMap())) {
      ObserverRuntime.emit(kind, old);
      assertTrue(after.drain().isEmpty());
      Map<String, Object> current = Collections.singletonMap("startedAt", java.time.Instant.now().toString());
      ObserverRuntime.emit(kind, current);
      assertEquals(Collections.singletonList(current), after.drain());
    }
  }

  @Test public void removesAFailedArmingAfterAnErrorAndAllowsTheSameCaptureIdToRetry() throws Exception {
    assertEquals("original:warm", new ExistingClient().request("warm"));
    String kind = "failed-arm-error";
    java.util.concurrent.atomic.AtomicBoolean first = new java.util.concurrent.atomic.AtomicBoolean(true);
    ObserverRuntime.register(new Adapter(kind) {
      public boolean matchesType(String name, Set<String> hierarchy) {
        if (!name.equals(ExistingClient.class.getName())) return false;
        if (first.getAndSet(false)) throw new InternalError("arming fixture error");
        return true;
      }
    });
    try { ObserverRuntime.subscribe(kind, kind, Collections.emptyMap()); fail("arming must preserve the original Error"); }
    catch (InternalError expected) { assertEquals("arming fixture error", expected.getMessage()); }
    assertFalse(ObserverRuntime.armed(kind));
    try (ObserverSubscription retry = ObserverRuntime.subscribe(kind, kind, Collections.emptyMap())) {
      assertEquals("original:retry", new ExistingClient().request("retry"));
      retry.check();
    }
  }

  @Test public void doesNotDispatchNativeOperationsToAnUnarmedAdapter() throws Exception {
    String active = "native-routing-active", inactive = "native-routing-inactive";
    java.util.concurrent.atomic.AtomicInteger inactiveCalls = new java.util.concurrent.atomic.AtomicInteger();
    ObserverRuntime.register(new Adapter(inactive) {
      public void onOperation(Map<String, Object> metadata) { inactiveCalls.incrementAndGet(); }
    });
    ObserverRuntime.register(new Adapter(active) {
      public void onOperation(Map<String, Object> metadata) { ObserverRuntime.emit(active, metadata); }
    });
    try (ObserverSubscription capture = ObserverRuntime.subscribe(active, active, Collections.emptyMap())) {
      Map<String, Object> metadata = Collections.singletonMap("operationId", "routing-fixture");
      ObserverRuntime.operation(metadata);
      assertEquals(Collections.singletonList(metadata), capture.drain());
      assertEquals(0, inactiveCalls.get());
    }
  }

  private static class Adapter implements ObserverAdapter {
    private final String kind;
    Adapter(String kind) { this.kind = kind; }
    public String id() { return kind; }
    public Set<String> kinds() { return Collections.singleton(kind); }
    public boolean matchesType(String name, Set<String> hierarchy) { return name.equals(ExistingClient.class.getName()); }
    public boolean matchesMethod(String name, String descriptor) { return name.equals("request"); }
    public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return args[0]; }
    public Object onExit(Object token, Object result, Throwable error) { return result; }
  }
}
