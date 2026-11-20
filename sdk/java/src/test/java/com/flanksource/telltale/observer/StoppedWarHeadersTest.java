package com.flanksource.telltale.observer;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.fixture.StoppedWebapp;
import framefixture.StoppedWarClient;
import com.flanksource.telltale.diag.hotpatch.BytecodeSource;
import java.lang.instrument.*;
import java.lang.reflect.Method;
import java.security.ProtectionDomain;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.apache.catalina.loader.WebappClassLoaderBase;
import org.junit.Test;

public class StoppedWarHeadersTest {
  @Test public void reportsRetiredWarTargetsWithoutRetransformingThemAndStillCapturesLiveWork() throws Exception {
    try (StoppedWebapp webapp = new StoppedWebapp(StoppedWarClient.class)) {
      Class<?> client = webapp.load(StoppedWarClient.class);
      assertSame(webapp.loader(), client.getClassLoader());
      Object receiver = client.getConstructor().newInstance();
      Method choose = client.getMethod("choose", boolean.class);
      assertEquals("IOException", choose.invoke(receiver, true).getClass().getSimpleName());
      assertEquals("SQLException", choose.invoke(receiver, false).getClass().getSimpleName());
      webapp.stop();
      assertFalse(((WebappClassLoaderBase) webapp.loader()).getState().isAvailable());
      String state = ((WebappClassLoaderBase) webapp.loader()).getState().name();
      Instrumentation installed = ByteBuddyAgent.install();
      AtomicInteger retransforms = new AtomicInteger();
      ClassFileTransformer counter = new ClassFileTransformer() {
        public byte[] transform(ClassLoader loader, String name, Class<?> type, ProtectionDomain domain, byte[] bytes) {
          if (type == client) retransforms.incrementAndGet();
          return null;
        }
      };
      installed.addTransformer(counter, true);
      try {
      ObserverRuntime.install(installed);
      ObserverRuntime.register(new ObserverAdapter() {
        public String id() { return "stopped-war-headers-test"; }
        public Set<String> kinds() { return Collections.singleton("stopped-war-headers"); }
        public boolean matchesType(String name, Set<String> hierarchy) { return name.equals(StoppedWarClient.class.getName()); }
        public boolean matchesMethod(String name, String descriptor) { return name.equals("choose"); }
        public Object onEnter(String owner, String name, String descriptor, Object target, Object[] args) { return Boolean.TRUE; }
        public Object onExit(Object token, Object result, Throwable error) {
          ObserverRuntime.emit("stopped-war-headers", Collections.singletonMap("type", result.getClass().getSimpleName()));
          return result;
        }
      });
      try (ObserverSubscription capture = ObserverRuntime.subscribe("stopped-war-headers", "stopped-war-headers", Collections.emptyMap())) {
        assertEquals("IOException", choose.invoke(receiver, true).getClass().getSimpleName());
        assertEquals("SQLException", choose.invoke(receiver, false).getClass().getSimpleName());
        assertTrue(capture.drain().isEmpty());
        StoppedWarClient live = new StoppedWarClient();
        assertEquals("IOException", live.choose(true).getClass().getSimpleName());
        assertEquals("SQLException", live.choose(false).getClass().getSimpleName());
        assertEquals(2, capture.drain().size());
        assertRetired(webapp.loader(), state);
        try { new BytecodeSource(installed).bytecodeOf(client); fail("retired WAR bytecode read must fail before native retransformation"); }
        catch (IllegalStateException expected) {
          assertTrue(expected.getMessage(), expected.getMessage().contains(client.getName()));
          assertTrue(expected.getMessage(), expected.getMessage().contains(state));
        }
      }
      assertEquals("retired classes must stay untouched during arm and teardown", 0, retransforms.get());
      } finally { installed.removeTransformer(counter); }
    }
  }

  @SuppressWarnings("unchecked") private static void assertRetired(ClassLoader loader, String state) {
    List<Map<String, Object>> retired = (List<Map<String, Object>>) ObserverRuntime.capabilities().get("retiredLoaders");
    for (Map<String, Object> entry : retired) {
      if (!Integer.toHexString(System.identityHashCode(loader)).equals(entry.get("id"))) continue;
      assertEquals(loader.getClass().getName(), entry.get("class")); assertEquals(state, entry.get("state"));
      assertTrue(((Number) entry.get("classes")).intValue() > 0); return;
    }
    fail("retired WAR loader missing from explicit capability diagnostics: " + retired);
  }
}
