package com.flanksource.telltale.observer;

import static org.junit.Assert.*;

import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;
import observerfixture.ExistingClient;

public class ObserverRuntimeTest {
  @Test
  public void observesAnAlreadyCreatedClientAndReleasesOnlyItsOwnHooks() throws Exception {
    ExistingClient client = new ExistingClient();
    assertEquals("original:warm", client.request("warm"));
    ObserverRuntime.install(ByteBuddyAgent.install());
    ObserverRuntime.register(new ObserverAdapter() {
      public String id() { return "existing-client-test"; }
      public Set<String> kinds() { return Collections.singleton("fixture"); }
      public boolean matchesType(String name, Set<String> hierarchy) {
        return name.equals(ExistingClient.class.getName());
      }
      public boolean matchesMethod(String name, String descriptor) { return name.equals("request"); }
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) {
        return args[0];
      }
      public Object onExit(Object token, Object result, Throwable error) {
        Map<String, Object> row = new LinkedHashMap<>();
        row.put("argument", token); row.put("result", result);
        row.put("error", error == null ? null : error.getMessage());
        ObserverRuntime.emit("fixture", row);
        return result;
      }
    });
    try (ObserverSubscription capture = ObserverRuntime.subscribe("fixture", "fixture", Collections.emptyMap())) {
      assertEquals("original:active", client.request("active"));
      try { client.request("fail"); fail("the original exception must propagate"); }
      catch (IllegalArgumentException expected) { assertEquals("original failure", expected.getMessage()); }
      List<Map<String, Object>> rows = capture.drain();
      assertEquals(2, rows.size());
      assertEquals("original:active", rows.get(0).get("result"));
      assertEquals("original failure", rows.get(1).get("error"));
    }
    assertEquals("original:released", client.request("released"));
    assertFalse(ObserverRuntime.armed("fixture"));
  }
}
