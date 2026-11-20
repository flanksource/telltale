package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import net.bytebuddy.agent.ByteBuddyAgent;
import observerfixture.ExistingClient;
import org.junit.Test;

public class ObserverRefcountTest {
  @Test public void sharesHooksAcrossKindsAndLeavesUnchangedAdaptersUntouched() throws Exception {
    ExistingClient client = new ExistingClient();
    ObserverRuntime.install(ByteBuddyAgent.install());
    AtomicInteger weaves = new AtomicInteger();
    ObserverRuntime.register(new ObserverAdapter() {
      public String id() { return "refcount"; }
      public Set<String> kinds() { return new HashSet<>(Arrays.asList("refcount-events", "refcount-top")); }
      public boolean matchesType(String name, Set<String> hierarchy) { return name.equals(ExistingClient.class.getName()); }
      public boolean matchesMethod(String name, String descriptor) {
        if (!name.equals("request")) return false;
        weaves.incrementAndGet(); return true;
      }
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return args[0]; }
      public Object onExit(Object token, Object result, Throwable error) {
        ObserverRuntime.emit("refcount-events", Collections.singletonMap("result", result)); return result;
      }
    });
    ObserverRuntime.register(new ObserverAdapter() {
      public String id() { return "unrelated-refcount"; }
      public Set<String> kinds() { return Collections.singleton("unrelated-refcount"); }
      public boolean matchesType(String name, Set<String> hierarchy) { return false; }
      public boolean matchesMethod(String name, String descriptor) { return false; }
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return null; }
      public Object onExit(Object token, Object result, Throwable error) { return result; }
    });
    try (ObserverSubscription events = ObserverRuntime.subscribe("refcount-events", "events", Collections.emptyMap())) {
      int initial = weaves.get();
      assertTrue("the preexisting client must be transformed", initial > 0);
      try (ObserverSubscription top = ObserverRuntime.subscribe("refcount-top", "top", Collections.emptyMap())) {
        assertEquals("a second kind must reuse the same adapter hooks", initial, weaves.get());
      }
      assertEquals("closing one kind must preserve its sibling hooks", initial, weaves.get());
      try (ObserverSubscription unrelated = ObserverRuntime.subscribe("unrelated-refcount", "unrelated", Collections.emptyMap())) {
        assertEquals("adding a disjoint adapter must not rewrite the client", initial, weaves.get());
      }
      assertEquals(initial, weaves.get());
      assertEquals("original:active", client.request("active"));
      assertEquals(Collections.singletonList(Collections.singletonMap("result", "original:active")), events.drain());
    }
    assertEquals("original:released", client.request("released"));
  }
}
