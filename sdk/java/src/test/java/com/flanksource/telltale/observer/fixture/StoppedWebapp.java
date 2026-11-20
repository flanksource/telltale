package com.flanksource.telltale.observer.fixture;

import java.io.*;
import java.nio.file.*;
import org.apache.catalina.Context;
import org.apache.catalina.startup.Tomcat;

public final class StoppedWebapp implements AutoCloseable {
  private final Tomcat tomcat;
  private final ClassLoader loader;
  private boolean stopped;

  public StoppedWebapp(Class<?>... fixtures) throws Exception {
    Path root = Paths.get(".tmp"); Files.createDirectories(root);
    Path directory = Files.createTempDirectory(root, "observer-war-lifecycle-");
    Path webapp = directory.resolve("webapp");
    Files.createDirectories(webapp.resolve("WEB-INF/classes"));
    for (Class<?> fixture : fixtures) {
      String resource = fixture.getName().replace('.', '/') + ".class";
      Path destination = webapp.resolve("WEB-INF/classes").resolve(resource);
      Files.createDirectories(destination.getParent());
      try (InputStream input = fixture.getClassLoader().getResourceAsStream(resource)) {
        if (input == null) throw new FileNotFoundException(resource);
        Files.copy(input, destination);
      }
    }
    tomcat = new Tomcat(); tomcat.setBaseDir(directory.resolve("tomcat").toAbsolutePath().toString());
    tomcat.setPort(0);
    Context context = tomcat.addContext("", webapp.toAbsolutePath().toString());
    context.setParentClassLoader(StoppedWebapp.class.getClassLoader());
    tomcat.start();
    loader = context.getLoader().getClassLoader();
  }

  public Class<?> load(Class<?> fixture) throws ClassNotFoundException { return loader.loadClass(fixture.getName()); }
  public ClassLoader loader() { return loader; }

  public void stop() throws Exception {
    if (stopped) return;
    try { tomcat.stop(); }
    finally { tomcat.destroy(); stopped = true; }
  }

  public void close() throws Exception { stop(); }
}
