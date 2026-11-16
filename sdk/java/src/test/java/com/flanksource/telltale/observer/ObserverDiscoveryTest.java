package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.io.*;
import java.net.*;
import java.util.*;
import observerfixture.ExistingClient;
import com.flanksource.telltale.observer.fixture.StoppedWebapp;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class ObserverDiscoveryTest {
  @Test public void armsCallbackDiscoveryAfterARealWebappStopsWithoutTouchingItsResources() throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (StoppedWebapp webapp = new StoppedWebapp(ExistingClient.class)) {
      Class<?> type = webapp.load(ExistingClient.class);
      Object client = type.getConstructor().newInstance();
      java.lang.reflect.Method request = type.getMethod("request", String.class);
      assertEquals("original:warm", request.invoke(client, "warm"));
      webapp.stop();
      assertFalse(((org.apache.catalina.loader.WebappClassLoaderBase) webapp.loader()).getState().isAvailable());
      try (ObserverSubscription http = ObserverRuntime.subscribe("http", "stopped-webapp-http", Collections.emptyMap());
          ObserverSubscription jms = ObserverRuntime.subscribe("jms", "stopped-webapp-jms", Collections.emptyMap())) {
        assertEquals("original:after-stop", request.invoke(client, "after-stop"));
        http.check(); jms.check();
        http.close();
        assertFalse(ObserverRuntime.armed("http"));
        assertTrue(ObserverRuntime.armed("jms"));
      }
      assertFalse(ObserverRuntime.armed("jms"));
    }
  }

  @Test public void indexesUnknownClassArtifactsWithoutReadingAStoppedLoader() throws Exception {
    try (StoppedLoader loader = new StoppedLoader()) {
      Class<?> type = loader.loadClass("observerfixture.ExistingClient");
      loader.close();
      assertFalse(new ObserverTransformer().candidate(type, Collections.singletonList(new Adapter()), false));
      assertEquals(0, loader.resourceReads);
    }
  }

  @Test public void discoversUnknownCallbackCallersThroughTheirArtifactWithoutReadingAStoppedLoader() throws Exception {
    try (StoppedLoader loader = new StoppedLoader()) {
      Class<?> type = loader.loadClass("observerfixture.JmsCallbackDispatcher");
      loader.close();
      assertTrue(new ObserverTransformer().candidate(type, Collections.singletonList(new Adapter()), false));
      assertEquals(0, loader.resourceReads);
    }
  }

  @Test public void excludesAnUnrelatedClassWhoseOptionalNativeInitializerFailed() throws Exception {
    Class<?> type = observerfixture.BrokenOptionalNative.class;
    try { Class.forName(type.getName(), true, type.getClassLoader()); fail("The optional native initializer must fail"); }
    catch (UnsatisfiedLinkError expected) { assertEquals("optional native fixture is unavailable", expected.getMessage()); }
    assertFalse(new ObserverTransformer().candidate(type, Collections.singletonList(new Adapter()), false));
  }

  @Test public void keepsAResourceFreeGeneratedCallerEligibleForJvmDiscovery() throws Exception {
    Class<?> type = new ArtifactLoader().define(null);
    assertTrue(new ObserverTransformer().candidate(type, Collections.singletonList(new Adapter()), false));
  }

  @Test public void reportsAnUnreadableCodeSourceInsteadOfSilentlyLosingCallbackDiscovery() throws Exception {
    URL unavailable = new File(".tmp/missing-callback-artifact-" + UUID.randomUUID()).toURI().toURL();
    Class<?> type = new ArtifactLoader().define(unavailable);
    try {
      new ObserverTransformer().candidate(type, Collections.singletonList(new Adapter()), false);
      fail("An unreadable class artifact must fail discovery");
    } catch (IllegalStateException expected) {
      assertTrue(expected.getMessage().contains(type.getName()));
      assertTrue(expected.getCause() instanceof IOException);
    }
  }

  @Test public void indexesPassiveClassLoadsAndSkipsKnownClassesWithoutCallbackSites() throws Exception {
    try (StoppedLoader loader = new StoppedLoader()) {
      Class<?> type = loader.loadClass("observerfixture.ExistingClient");
      ObserverTransformer transformer = new ObserverTransformer();
      assertNull(transformer.transform(loader, "observerfixture/ExistingClient", null, null,
          bytes("observerfixture/ExistingClient.class")));
      loader.close();
      assertFalse(transformer.candidate(type, Collections.singletonList(new Adapter()), false));
      assertEquals(0, loader.resourceReads);
    }
  }

  @Test public void retainsDiscoveredJmsCallbackSitesAfterTheirLoaderStops() throws Exception {
    try (StoppedLoader loader = new StoppedLoader()) {
      Class<?> type = loader.loadClass("observerfixture.JmsCallbackDispatcher");
      ObserverTransformer transformer = new ObserverTransformer();
      assertNull(transformer.transform(loader, "observerfixture/JmsCallbackDispatcher", null, null,
          bytes("observerfixture/JmsCallbackDispatcher.class")));
      loader.close();
      assertTrue(transformer.candidate(type, Collections.singletonList(new Adapter()), false));
      assertEquals(0, loader.resourceReads);
    }
  }

  private static byte[] bytes(String path) throws IOException {
    try (InputStream input = ObserverDiscoveryTest.class.getClassLoader().getResourceAsStream(path);
        ByteArrayOutputStream output = new ByteArrayOutputStream()) {
      if (input == null) throw new IllegalStateException("missing fixture " + path);
      byte[] buffer = new byte[4096];
      for (int read; (read = input.read(buffer)) != -1;) output.write(buffer, 0, read);
      return output.toByteArray();
    }
  }

  private static final class StoppedLoader extends URLClassLoader {
    boolean stopped;
    int resourceReads;
    StoppedLoader() {
      super(new URL[] {ExistingClient.class.getProtectionDomain().getCodeSource().getLocation()},
          ObserverDiscoveryTest.class.getClassLoader());
    }
    protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!name.startsWith("observerfixture.")) return super.loadClass(name, resolve);
      synchronized (getClassLoadingLock(name)) {
        Class<?> type = findLoadedClass(name);
        if (type == null) type = findClass(name);
        if (resolve) resolveClass(type);
        return type;
      }
    }
    public InputStream getResourceAsStream(String name) {
      resourceReads++;
      if (stopped) throw new IllegalStateException("web application loader has stopped: " + name);
      return super.getResourceAsStream(name);
    }
    public void close() throws IOException { stopped = true; super.close(); }
  }

  private static final class ArtifactLoader extends java.security.SecureClassLoader {
    Class<?> define(URL artifact) throws IOException {
      byte[] bytes = bytes("observerfixture/JmsCallbackDispatcher.class");
      return defineClass("observerfixture.JmsCallbackDispatcher", bytes, 0, bytes.length,
          new java.security.CodeSource(artifact, (java.security.cert.Certificate[]) null));
    }
  }

  private static final class Adapter implements ObserverAdapter {
    public String id() { return "callback-discovery"; }
    public Set<String> kinds() { return Collections.singleton("callback-discovery"); }
    public boolean matchesType(String name, Set<String> hierarchy) { return false; }
    public boolean matchesMethod(String name, String descriptor) { return false; }
    public boolean matchesInvocation(String owner, String name, String descriptor) {
      return owner.equals("javax.jms.MessageListener") && name.equals("onMessage");
    }
    public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return null; }
    public Object onExit(Object token, Object result, Throwable error) { return result; }
  }
}
