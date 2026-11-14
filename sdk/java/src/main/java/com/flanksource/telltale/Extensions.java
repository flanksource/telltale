package com.flanksource.telltale;

import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;

final class Extensions implements AutoCloseable {
  private final Map<String, Loaded> loaded = new LinkedHashMap<>();
  private final Path directory;

  private static final class Loaded implements AutoCloseable {
    final DiagnosticExtension extension;
    final URLClassLoader loader;
    final ExtensionContext context;
    final String digest;
    int active;
    boolean failed;
    boolean extensionClosed;
    boolean contextClosed;

    Loaded(
        DiagnosticExtension extension,
        URLClassLoader loader,
        ExtensionContext context,
        String digest) {
      this.extension = extension;
      this.loader = loader;
      this.context = context;
      this.digest = digest;
    }

    public void close() throws Exception {
      failed = true;
      if (!extensionClosed) {
        extension.close();
        extensionClosed = true;
      }
      if (!contextClosed) {
        context.close();
        contextClosed = true;
      }
      loader.close();
    }
  }

  Extensions(String directory) throws Exception {
    this.directory = directory == null ? null : Paths.get(directory);
    if (this.directory != null && !this.directory.isAbsolute())
      throw new IllegalArgumentException("extensionsDir must be absolute");
    if (this.directory == null || !Files.exists(this.directory.resolve("active.properties")))
      return;
    Properties active = new Properties();
    try (InputStream input = Files.newInputStream(this.directory.resolve("active.properties"))) {
      active.load(input);
    }
    try {
      for (String id : active.stringPropertyNames()) {
        String digest = active.getProperty(id);
        if (!digest.matches("[a-f0-9]{64}"))
          throw new IllegalStateException("invalid extension digest for " + id);
        Path jar = this.directory.resolve(digest + ".jar");
        if (!digest.equals(digest(Files.readAllBytes(jar))))
          throw new IllegalStateException("extension digest mismatch for " + id);
        Loaded item = load(jar, digest);
        if (!id.equals(item.extension.id())) {
          item.close();
          throw new IllegalStateException("extension identity mismatch for " + id);
        }
        loaded.put(id, item);
      }
    } catch (Exception error) {
      try {
        close();
      } catch (Exception cleanup) {
        error.addSuppressed(cleanup);
      }
      throw error;
    }
  }

  synchronized List<Map<String, Object>> list() {
    List<Map<String, Object>> result = new ArrayList<>();
    for (Loaded item : loaded.values()) {
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("id", item.extension.id());
      row.put("version", item.extension.version());
      row.put("digest", item.digest);
      row.put("active", item.active);
      row.put("status", item.failed ? "failed" : "active");
      row.put("operations", item.context.operations.keySet());
      result.add(row);
    }
    return result;
  }

  synchronized List<Map<String, Object>> catalog() {
    List<Map<String, Object>> result = new ArrayList<>();
    for (Loaded item : loaded.values())
      if (!item.failed)
        for (OperationSpec operation : item.context.operations.values()) {
          Map<String, Object> spec = operation.descriptor();
          spec.put("extension", item.extension.id());
          result.add(spec);
        }
    return result;
  }

  synchronized Object install(Map<String, Object> params, boolean replace) throws Exception {
    if (directory == null)
      throw new IllegalStateException("an absolute extensionsDir must be configured");
    String encoded = Diagnostics.required(params, "jar");
    if (encoded.length() > 24 * 1024 * 1024)
      throw new IllegalArgumentException("extension exceeds 16 MiB");
    byte[] bytes = Base64.getDecoder().decode(encoded);
    if (bytes.length > 16 * 1024 * 1024)
      throw new IllegalArgumentException("extension exceeds 16 MiB");
    Files.createDirectories(directory);
    String digest = digest(bytes);
    Path jar = directory.resolve(digest + ".jar");
    if (!Files.exists(jar)) Files.write(jar, bytes, StandardOpenOption.CREATE_NEW);
    if (!digest.equals(digest(Files.readAllBytes(jar))))
      throw new IllegalStateException("stored extension digest mismatch");
    Loaded candidate = load(jar, digest);
    boolean accepted = false;
    try {
      String id = candidate.extension.id();
      Loaded existing = loaded.get(id);
      if (existing != null) {
        if (!replace)
          throw new IllegalStateException("extension already installed; use replace: " + id);
        if (existing.active != 0)
          throw new IllegalStateException("extension has active calls or scripts: " + id);
        existing.close();
      }
      Map<String, Loaded> planned = new LinkedHashMap<>(loaded);
      planned.put(id, candidate);
      persist(planned);
      loaded.put(id, candidate);
      accepted = true;
      return Collections.singletonMap("installed", id);
    } catch (Exception | LinkageError | ServiceConfigurationError error) {
      if (!accepted)
        try {
          candidate.close();
        } catch (Exception cleanup) {
          error.addSuppressed(cleanup);
        }
      if (error instanceof ServiceConfigurationError || error instanceof LinkageError)
        throw new IllegalArgumentException("load extension provider: " + error, error);
      throw error;
    }
  }

  synchronized Object unload(String id) throws Exception {
    Loaded item = loaded.get(id);
    if (item == null) throw new IllegalArgumentException("unknown extension: " + id);
    if (item.active != 0)
      throw new IllegalStateException("extension has active calls or scripts: " + id);
    item.close();
    Map<String, Loaded> planned = new LinkedHashMap<>(loaded);
    planned.remove(id);
    persist(planned);
    loaded.remove(id);
    return Collections.singletonMap("unloaded", id);
  }

  Object call(String name, Map<String, Object> params, boolean allowed) throws Exception {
    Loaded owner = null;
    OperationSpec operation = null;
    synchronized (this) {
      for (Loaded item : loaded.values()) {
        operation = item.context.operations.get(name);
        if (operation != null) {
          if (item.failed)
            throw new IllegalStateException("extension cleanup failed: " + item.extension.id());
          if (operation.mutates && !allowed)
            throw new IllegalStateException("mutation disabled for " + name);
          owner = item;
          item.active++;
          break;
        }
      }
    }
    if (owner == null) throw new IllegalArgumentException("unknown operation: " + name);
    try {
      return operation.handler.call(params);
    } finally {
      synchronized (this) {
        owner.active--;
      }
    }
  }

  final class BindingsLease implements AutoCloseable {
    final Map<String, Object> values = new LinkedHashMap<>();
    final List<Loaded> owners = new ArrayList<>();

    public void close() {
      synchronized (Extensions.this) {
        for (Loaded item : owners) item.active--;
        owners.clear();
      }
    }
  }

  synchronized BindingsLease bindings(ClassLoader application) throws Exception {
    BindingsLease lease = new BindingsLease();
    try {
      for (Loaded item : loaded.values()) {
        if (item.failed)
          throw new IllegalStateException("extension cleanup failed: " + item.extension.id());
        item.active++;
        lease.owners.add(item);
        lease.values.put(item.extension.id(), item.context.bindings(application));
      }
      return lease;
    } catch (Exception error) {
      lease.close();
      throw error;
    }
  }

  public synchronized void close() throws Exception {
    Exception failure = null;
    for (Loaded item : loaded.values())
      try {
        if (item.active != 0)
          throw new IllegalStateException("extension is busy: " + item.extension.id());
        item.close();
      } catch (Exception error) {
        if (failure == null) failure = error;
        else failure.addSuppressed(error);
      }
    if (failure != null) throw failure;
    loaded.clear();
  }

  private Loaded load(Path jar, String digest) throws Exception {
    URLClassLoader loader =
        new URLClassLoader(
            new URL[] {jar.toUri().toURL()}, DiagnosticExtension.class.getClassLoader()) {
          public Enumeration<URL> getResources(String name) throws IOException {
            if (name.equals("META-INF/services/" + DiagnosticExtension.class.getName()))
              return findResources(name);
            return super.getResources(name);
          }

          protected synchronized Class<?> loadClass(String name, boolean resolve)
              throws ClassNotFoundException {
            if (name.startsWith("java.")
                || name.startsWith("javax.")
                || name.startsWith("com.flanksource.telltale."))
              return super.loadClass(name, resolve);
            Class<?> type = findLoadedClass(name);
            if (type == null)
              try {
                type = findClass(name);
              } catch (ClassNotFoundException absent) {
                type = super.loadClass(name, false);
              }
            if (resolve) resolveClass(type);
            return type;
          }
        };
    ExtensionContext context = new ExtensionContext();
    DiagnosticExtension extension = null;
    try {
      Iterator<DiagnosticExtension> providers =
          ServiceLoader.load(DiagnosticExtension.class, loader).iterator();
      if (!providers.hasNext())
        throw new IllegalArgumentException("JAR has no DiagnosticExtension provider");
      extension = providers.next();
      if (providers.hasNext())
        throw new IllegalArgumentException("JAR must contain exactly one extension");
      if (extension.getClass().getClassLoader() != loader)
        throw new IllegalArgumentException("extension provider must be defined in its JAR");
      if (extension.apiVersion() != 1
          || !extension.id().matches("[a-z][a-z0-9-]*")
          || extension.version().trim().isEmpty())
        throw new IllegalArgumentException("invalid extension API, identity or version");
      extension.start(context);
      for (String name : context.operations.keySet())
        if (!name.startsWith("extension." + extension.id() + "."))
          throw new IllegalArgumentException("extension operation must use its namespace");
      return new Loaded(extension, loader, context, digest);
    } catch (Exception | LinkageError | ServiceConfigurationError error) {
      if (extension != null)
        try {
          extension.close();
        } catch (Exception cleanup) {
          error.addSuppressed(cleanup);
        }
      try {
        context.close();
        loader.close();
      } catch (Exception cleanup) {
        error.addSuppressed(cleanup);
      }
      if (error instanceof ServiceConfigurationError || error instanceof LinkageError)
        throw new IllegalArgumentException("load extension provider: " + error, error);
      throw error;
    }
  }

  private void persist(Map<String, Loaded> planned) throws IOException {
    Properties active = new Properties();
    for (Loaded item : planned.values()) active.setProperty(item.extension.id(), item.digest);
    Path staged = Files.createTempFile(directory, "active-", ".properties");
    try {
      try (OutputStream output = Files.newOutputStream(staged)) {
        active.store(output, "Telltale active extensions");
      }
      Files.move(
          staged,
          directory.resolve("active.properties"),
          StandardCopyOption.ATOMIC_MOVE,
          StandardCopyOption.REPLACE_EXISTING);
    } finally {
      Files.deleteIfExists(staged);
    }
  }

  private static String digest(byte[] bytes) throws Exception {
    StringBuilder result = new StringBuilder();
    for (byte value : MessageDigest.getInstance("SHA-256").digest(bytes))
      result.append(String.format("%02x", value & 255));
    return result.toString();
  }
}
