package com.flanksource.telltale.observer;

import static org.junit.Assert.*;

import java.io.*;
import java.lang.ref.WeakReference;
import java.lang.reflect.Method;
import java.net.*;
import java.nio.file.*;
import java.util.*;
import java.util.jar.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class ObserverFrameLifetimeTest {
  private static final String CLIENT = "framefixture.ClosedLoaderClient";

  @Test public void observesAlreadyLoadedClassesAfterTheirJarLoaderCloses() throws Exception {
    Path jar = fixture();
    URLClassLoader loader = loader(jar);
    Class<?> client = loader.loadClass(CLIENT);
    Method choose = client.getMethod("choose", boolean.class);
    assertEquals("First", choose.invoke(null, true).getClass().getSimpleName());
    assertEquals("Second", choose.invoke(null, false).getClass().getSimpleName());
    loader.close();
    assertNull(loader.getResource(CLIENT.replace('.', '/') + ".class"));
    register();
    try (ObserverSubscription capture = ObserverRuntime.subscribe("closed-loader", "closed-loader", Collections.emptyMap())) {
      assertEquals("First", choose.invoke(null, true).getClass().getSimpleName());
      assertEquals("Second", choose.invoke(null, false).getClass().getSimpleName());
      assertEquals(2, capture.drain().size());
    }
  }

  private static void register() throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    ObserverRuntime.register(new ObserverAdapter() {
      public String id() { return "closed-loader-test"; }
      public Set<String> kinds() { return Collections.singleton("closed-loader"); }
      public boolean matchesType(String name, Set<String> hierarchy) { return name.equals(CLIENT); }
      public boolean matchesMethod(String name, String descriptor) { return name.equals("choose"); }
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return Boolean.TRUE; }
      public Object onExit(Object token, Object result, Throwable error) {
        ObserverRuntime.emit("closed-loader", Collections.singletonMap("result", result.getClass().getSimpleName()));
        return result;
      }
    });
  }

  @Test public void observingDoesNotRetainAnAbandonedLoader() throws Exception {
    register();
    WeakReference<ClassLoader> reference = observedLoader(fixture());
    for (int attempt = 0; attempt < 80 && reference.get() != null; attempt++) {
      System.gc(); Thread.sleep(25);
    }
    assertNull("observer transformation must not retain the application's classloader", reference.get());
  }

  private static WeakReference<ClassLoader> observedLoader(Path jar) throws Exception {
    URLClassLoader loader = loader(jar);
    Method choose = loader.loadClass(CLIENT).getMethod("choose", boolean.class);
    try (ObserverSubscription capture = ObserverRuntime.subscribe("closed-loader", "abandoned-loader", Collections.emptyMap())) {
      assertEquals("First", choose.invoke(null, true).getClass().getSimpleName());
      assertEquals(1, capture.drain().size());
    }
    loader.close();
    return new WeakReference<ClassLoader>(loader);
  }

  private static URLClassLoader loader(Path jar) throws MalformedURLException {
    return new URLClassLoader(new URL[] {jar.toUri().toURL()}, ClassLoader.getSystemClassLoader().getParent());
  }

  private static Path fixture() throws IOException {
    Path root = Paths.get(".tmp"); Files.createDirectories(root);
    Path path = Files.createTempFile(root, "closed-frame-loader-", ".jar");
    try (JarOutputStream jar = new JarOutputStream(Files.newOutputStream(path))) {
      for (String suffix : new String[] {"", "$Base", "$First", "$Second"}) {
        jar.putNextEntry(new JarEntry((CLIENT + suffix).replace('.', '/') + ".class"));
        jar.write(resource(CLIENT + suffix)); jar.closeEntry();
      }
    }
    return path;
  }

  private static byte[] resource(String name) throws IOException {
    try (InputStream input = ObserverFrameLifetimeTest.class.getClassLoader().getResourceAsStream(name.replace('.', '/') + ".class")) {
      if (input == null) throw new FileNotFoundException(name);
      ByteArrayOutputStream output = new ByteArrayOutputStream(); byte[] buffer = new byte[8192];
      for (int count; (count = input.read(buffer)) != -1;) output.write(buffer, 0, count);
      return output.toByteArray();
    }
  }

}
