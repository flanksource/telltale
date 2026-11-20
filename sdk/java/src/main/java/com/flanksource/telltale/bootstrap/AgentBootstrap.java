package com.flanksource.telltale.bootstrap;

import java.lang.instrument.Instrumentation;
import java.lang.management.ManagementFactory;
import java.lang.reflect.*;
import java.net.*;
import java.nio.file.*;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.jar.JarFile;
import javax.management.ObjectName;

public final class AgentBootstrap {
  private static RuntimeVersion current;
  private AgentBootstrap() {}

  public static void agentmain(String options, Instrumentation instrumentation) throws Exception {
    String[] args = options.split("\n", -1);
    if (args.length != 4 || !(args[2].equals("true") || args[2].equals("false")))
      throw new IllegalArgumentException("Expected configuration path, artifact path, upgrade flag and result path");
    Path result = Paths.get(args[3]);
    try {
      install(Paths.get(args[1]), Paths.get(args[0]), instrumentation, Boolean.parseBoolean(args[2]));
      Files.write(result, "ok".getBytes(StandardCharsets.UTF_8));
    } catch (Exception | LinkageError failure) {
      java.io.StringWriter report = new java.io.StringWriter();
      failure.printStackTrace(new java.io.PrintWriter(report));
      try { Files.write(result, ("error\n" + report).getBytes(StandardCharsets.UTF_8)); }
      catch (Exception reporting) { failure.addSuppressed(reporting); }
      throw failure;
    }
  }

  public static void premain(String config, Instrumentation instrumentation) throws Exception {
    Path artifact = Paths.get(AgentBootstrap.class.getProtectionDomain().getCodeSource().getLocation().toURI());
    install(artifact, Paths.get(config), instrumentation, false);
  }

  public static synchronized void install(Path artifact, Path config, Instrumentation instrumentation, boolean upgrade) throws Exception {
    String digest = digest(artifact);
    if (current != null && !upgrade) {
      if (!current.digest.equals(digest)) throw new IllegalStateException("agent artifact changed; use --upgrade to replace the runtime");
      current.invoke("validateRuntime", new Class<?>[] {String.class}, config.toString());
      current.invoke("agentmain", new Class<?>[] {String.class, Instrumentation.class}, config.toString(), instrumentation);
      return;
    }
    RuntimeVersion candidate = new RuntimeVersion(artifact, digest);
    RuntimeVersion previous = current;
    boolean activated = false;
    try {
      candidate.invoke("validateRuntime", new Class<?>[] {String.class}, config.toString());
      if (current == null && ManagementFactory.getPlatformMBeanServer().isRegistered(new ObjectName("com.flanksource.telltale:type=Diagnostics")))
        throw new IllegalStateException("the loaded agent predates runtime upgrades; restart the JVM once to install the upgrade-capable launcher");
      if (current != null) {
        current.invoke("prepareUpgrade", new Class<?>[0]);
        current.invoke("stopRuntime", new Class<?>[0]);
      }
      try {
        candidate.start(config, instrumentation);
      } catch (Exception | LinkageError failure) {
        current = null;
        try {
          candidate.invoke("stopRuntime", new Class<?>[0]);
          if (previous != null) {
            RuntimeVersion restored = new RuntimeVersion(previous.artifact, previous.digest);
            Path saved = Files.createTempFile(previous.artifact.getParent(), "rollback-", ".json");
            try {
              Files.write(saved, previous.configData);
              restored.start(saved, instrumentation);
            } finally { Files.deleteIfExists(saved); }
            current = restored;
          }
        } catch (Exception | LinkageError recovery) { failure.addSuppressed(recovery); }
        throw new IllegalStateException(current == null ? "agent activation failed; no runtime is active" : "agent upgrade failed; the previous runtime was restored", failure);
      }
      current = candidate;
      activated = true;
    } finally {
      if (!activated) candidate.loader.close();
    }
  }

  private static String digest(Path artifact) throws Exception {
    try (JarFile jar = new JarFile(artifact.toFile())) {
      if (jar.getJarEntry("com/flanksource/telltale/Agent.class") == null)
        throw new java.io.IOException("agent artifact has no runtime entry point");
    }
    byte[] hash = MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(artifact));
    StringBuilder value = new StringBuilder();
    for (byte part : hash) value.append(String.format("%02x", part & 255));
    return value.toString();
  }

  private static final class RuntimeVersion {
    final URLClassLoader loader;
    final Class<?> entry;
    final String digest;
    final Path artifact;
    byte[] configData;

    RuntimeVersion(Path artifact, String digest) throws Exception {
      this.digest = digest;
      this.artifact = artifact;
      loader = new URLClassLoader(new URL[] {artifact.toUri().toURL()}, ClassLoader.getSystemClassLoader().getParent()) {
        protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
          if (name.startsWith("com.flanksource.telltale.diag.agent.") || name.startsWith("com.flanksource.telltale.bootstrap."))
            return ClassLoader.getSystemClassLoader().loadClass(name);
          return super.loadClass(name, resolve);
        }
      };
      try {
        entry = loader.loadClass("com.flanksource.telltale.Agent");
        entry.getMethod("validateRuntime", String.class);
        entry.getMethod("agentmain", String.class, Instrumentation.class);
        entry.getMethod("prepareUpgrade");
        entry.getMethod("stopRuntime");
      } catch (Exception | LinkageError error) {
        loader.close();
        throw error;
      }
    }

    void start(Path config, Instrumentation instrumentation) throws Exception {
      configData = Files.readAllBytes(config);
      invoke("agentmain", new Class<?>[] {String.class, Instrumentation.class}, config.toString(), instrumentation);
    }

    Object invoke(String method, Class<?>[] types, Object... args) throws Exception {
      try { return entry.getMethod(method, types).invoke(null, args); }
      catch (InvocationTargetException error) {
        Throwable cause = error.getCause();
        if (cause instanceof Error) throw (Error) cause;
        if (cause instanceof Exception) throw (Exception) cause;
        throw new IllegalStateException("runtime lifecycle failed: " + method, cause);
      }
    }
  }
}
