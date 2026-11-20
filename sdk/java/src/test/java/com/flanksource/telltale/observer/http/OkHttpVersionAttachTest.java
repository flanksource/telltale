package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import java.io.*;
import java.lang.reflect.InvocationTargetException;
import java.net.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class OkHttpVersionAttachTest {
  @Test void observesPreexistingOkHttpThreeCalls() throws Exception { version("3", "3.14.9"); }
  @Test void observesPreexistingOkHttpFourCalls() throws Exception { version("4", "4.12.0"); }

  private static void version(String major, String version) throws Exception {
    String classpath = System.getProperty("telltale.http.clients" + major + ".classpath");
    assertNotNull(classpath);
    List<URL> urls = new ArrayList<>();
    urls.add(OkHttpVersionAttachTest.class.getProtectionDomain().getCodeSource().getLocation());
    for (String entry : classpath.split(File.pathSeparator)) urls.add(new File(entry).toURI().toURL());
    ClassLoader previous = Thread.currentThread().getContextClassLoader();
    try (ClientLoader loader = new ClientLoader(urls.toArray(new URL[0]))) {
      Thread.currentThread().setContextClassLoader(loader);
      Class<?> fixture = loader.loadClass("observerfixture.http.OkHttpVersionFixture");
      assertSame(loader, fixture.getClassLoader());
      try { fixture.getMethod("run", String.class).invoke(null, version); }
      catch (InvocationTargetException failure) {
        if (failure.getCause() instanceof Exception) throw (Exception) failure.getCause();
        if (failure.getCause() instanceof Error) throw (Error) failure.getCause();
        throw failure;
      }
    } finally { Thread.currentThread().setContextClassLoader(previous); }
  }

  private static final class ClientLoader extends URLClassLoader {
    ClientLoader(URL[] urls) { super(urls, OkHttpVersionAttachTest.class.getClassLoader()); }
    @Override protected synchronized Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!owns(name)) return super.loadClass(name, resolve);
      Class<?> type = findLoadedClass(name);
      if (type == null) type = findClass(name);
      if (resolve) resolveClass(type);
      return type;
    }
    @Override public URL getResource(String name) { return owns(name.replace('/', '.')) ? findResource(name) : super.getResource(name); }
    private static boolean owns(String name) {
      return name.startsWith("observerfixture.http.") || name.startsWith("okhttp3.") || name.startsWith("okio.") || name.startsWith("kotlin.");
    }
  }
}
