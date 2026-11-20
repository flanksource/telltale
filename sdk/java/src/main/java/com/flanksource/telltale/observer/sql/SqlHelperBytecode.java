package com.flanksource.telltale.observer.sql;

import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.util.*;
import java.util.jar.*;
import java.util.stream.Stream;

final class SqlHelperBytecode {
  private final Map<String, byte[]> classes = new LinkedHashMap<>();
  private int bytes;

  static Map<String, byte[]> load(ClassLoader source) {
    SqlHelperBytecode cache = new SqlHelperBytecode();
    cache.load(source, "com/flanksource/telltale/observer/sql/R2dbcBridge.class", "com/flanksource/telltale/observer/sql/R2dbcBridge");
    cache.load(source, "io/r2dbc/proxy/callback/ProxyConfig.class", "io/r2dbc/proxy/");
    return Collections.unmodifiableMap(cache.classes);
  }

  private void load(ClassLoader source, String marker, String prefix) {
    URL resource = source.getResource(marker);
    if (resource == null) throw new IllegalStateException("SQL helper bytecode is missing: " + marker);
    try {
      if (resource.getProtocol().equals("jar")) {
        URL archive = ((JarURLConnection) resource.openConnection()).getJarFileURL();
        try (JarFile jar = new JarFile(Paths.get(archive.toURI()).toFile())) {
          Enumeration<JarEntry> entries = jar.entries();
          while (entries.hasMoreElements()) {
            JarEntry entry = entries.nextElement();
            if (matches(entry.getName(), prefix)) try (InputStream input = jar.getInputStream(entry)) { add(entry.getName(), input); }
          }
        }
      } else if (resource.getProtocol().equals("file")) {
        Path root = Paths.get(resource.toURI());
        for (String ignored : marker.split("/")) root = root.getParent();
        Path base = root;
        Path directory = base.resolve(prefix.substring(0, prefix.lastIndexOf('/')));
        try (Stream<Path> paths = Files.walk(directory)) {
          Iterator<Path> entries = paths.filter(Files::isRegularFile).iterator();
          while (entries.hasNext()) {
            Path path = entries.next();
            String name = base.relativize(path).toString().replace(File.separatorChar, '/');
            if (matches(name, prefix)) try (InputStream input = Files.newInputStream(path)) { add(name, input); }
          }
        }
      } else throw new IllegalStateException("unsupported SQL helper resource protocol: " + resource.getProtocol());
    } catch (IOException | URISyntaxException error) { throw new IllegalStateException("cache SQL helper bytecode: " + marker, error); }
    if (!classes.containsKey(marker.substring(0, marker.length() - 6).replace('/', '.')))
      throw new IllegalStateException("SQL helper bytecode root omitted marker: " + marker);
  }

  private static boolean matches(String name, String prefix) {
    return name.endsWith(".class") && (prefix.endsWith("/") ? name.startsWith(prefix) : name.equals(prefix + ".class") || name.startsWith(prefix + "$"));
  }

  private void add(String resource, InputStream input) throws IOException {
    if (classes.size() >= 1024) throw new IllegalStateException("SQL helper bytecode class limit exceeded");
    ByteArrayOutputStream output = new ByteArrayOutputStream();
    byte[] buffer = new byte[8192];
    for (int count; (count = input.read(buffer)) != -1;) {
      bytes += count;
      if (bytes > 8 * 1024 * 1024) throw new IllegalStateException("SQL helper bytecode byte limit exceeded");
      output.write(buffer, 0, count);
    }
    classes.put(resource.substring(0, resource.length() - 6).replace('/', '.'), output.toByteArray());
  }
}
