package observerfixture;

import static org.junit.Assert.*;

import java.io.*;
import java.lang.reflect.InvocationTargetException;
import java.net.*;
import java.util.*;

public final class SpringVersionFixture {
  private SpringVersionFixture() {}

  public static Class<?> define(ClassLoader loader, String name, byte[] bytes) {
    if (!(loader instanceof SpringLoader)) throw new IllegalArgumentException("a generated Spring contract requires its application fixture loader");
    return ((SpringLoader) loader).define(name, bytes);
  }

  public static void run(String property, String version, String type, String method) throws Exception {
    String classpath = System.getProperty(property);
    assertNotNull("version fixture classpath must be configured: " + property, classpath);
    assertFalse("version fixture must contain its real dependencies", classpath.isEmpty());
    List<URL> urls = new ArrayList<>();
    urls.add(SpringVersionFixture.class.getProtectionDomain().getCodeSource().getLocation());
    for (String entry : classpath.split(File.pathSeparator)) urls.add(new File(entry).toURI().toURL());
    ClassLoader previous = Thread.currentThread().getContextClassLoader();
    try (SpringLoader loader = new SpringLoader(urls.toArray(new URL[0]))) {
      Thread.currentThread().setContextClassLoader(loader);
      Class<?> fixture = loader.loadClass(type);
      assertSame("the fixture must use its isolated Spring dependency graph", loader, fixture.getClassLoader());
      Object actualVersion = loader.loadClass("org.springframework.core.SpringVersion").getMethod("getVersion").invoke(null);
      assertTrue("expected Spring " + version + " but got " + actualVersion, String.valueOf(actualVersion).startsWith(version));
      try { fixture.getMethod(method).invoke(null); }
      catch (InvocationTargetException error) {
        Throwable cause = error.getCause();
        if (cause instanceof Exception) throw (Exception) cause;
        if (cause instanceof Error) throw (Error) cause;
        throw error;
      }
    } finally { Thread.currentThread().setContextClassLoader(previous); }
  }

  private static final class SpringLoader extends URLClassLoader {
    SpringLoader(URL[] urls) { super(urls, SpringVersionFixture.class.getClassLoader()); }

    Class<?> define(String name, byte[] bytes) { return defineClass(name, bytes, 0, bytes.length); }

    @Override protected synchronized Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!owns(name)) return super.loadClass(name, resolve);
      Class<?> type = findLoadedClass(name);
      if (type == null) type = findClass(name);
      if (resolve) resolveClass(type);
      return type;
    }

    @Override public URL getResource(String name) {
      return ownsResource(name) ? findResource(name) : super.getResource(name);
    }

    @Override public Enumeration<URL> getResources(String name) throws IOException {
      return ownsResource(name) ? findResources(name) : super.getResources(name);
    }

    private static boolean ownsResource(String name) {
      return name.startsWith("META-INF/spring") || owns(name.replace('/', '.'));
    }

    private static boolean owns(String name) {
      return name.startsWith("org.springframework.") || name.startsWith("reactor.") || name.startsWith("io.netty.")
          || name.startsWith("org.reactivestreams.") || name.startsWith("io.micrometer.")
          || name.startsWith("org.apache.catalina.") || name.startsWith("org.apache.coyote.")
          || name.startsWith("org.apache.tomcat.") || name.startsWith("org.apache.juli.")
          || name.startsWith("org.apache.naming.") || name.startsWith("jakarta.servlet.") || name.startsWith("jakarta.annotation.")
          || name.startsWith("observerfixture.spring");
    }
  }
}
