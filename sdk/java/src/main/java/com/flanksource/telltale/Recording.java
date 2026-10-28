package com.flanksource.telltale;

import com.google.gson.JsonObject;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.atomic.AtomicBoolean;

final class Recording implements AutoCloseable {
  private static final AtomicBoolean ASYNC = new AtomicBoolean();
  private final Object recorder;
  private final Class<?> type;
  private final boolean async;
  private final Path file;
  private boolean closed;

  Recording(JsonObject params) throws Exception {
    String engine = value(params, "engine", "jfr");
    if (!engine.equals("jfr") && !engine.equals("async-profiler"))
      throw new IllegalArgumentException("Unknown JFR engine: " + engine);
    async = engine.equals("async-profiler");
    file = Files.createTempFile("telltale-recording-", ".jfr");
    boolean acquired = false;
    try {
      if (async) {
        if (!ASYNC.compareAndSet(false, true))
          throw new IllegalStateException("async-profiler is owned by another Telltale capture");
        acquired = true;
        type = Class.forName("one.profiler.AsyncProfiler");
        String library = System.getProperty("telltale.asyncProfiler.library");
        recorder =
            library == null
                ? type.getMethod("getInstance").invoke(null)
                : type.getMethod("getInstance", String.class).invoke(null, library);
        String status = execute("status");
        if (!status.toLowerCase(java.util.Locale.ROOT).contains("not active"))
          throw new IllegalStateException("async-profiler is already active: " + status);
        String event = value(params, "event", "");
        if (!java.util.Arrays.asList("cpu", "wall", "alloc", "lock").contains(event))
          throw new IllegalArgumentException("Unsupported async-profiler event: " + event);
        String interval = value(params, "interval", "");
        if (!interval.isEmpty() && !interval.matches("[0-9]+(ns|us|ms|s)?"))
          throw new IllegalArgumentException("Invalid profiler interval");
        execute(
            "start,jfr,event="
                + event
                + ",file="
                + file.toString()
                + (interval.isEmpty() ? "" : ",interval=" + interval));
      } else {
        Class<?> configType = Class.forName("jdk.jfr.Configuration");
        Object config =
            configType
                .getMethod("getConfiguration", String.class)
                .invoke(null, value(params, "settings", "profile"));
        type = Class.forName("jdk.jfr.Recording");
        recorder = type.getConstructor(configType).newInstance(config);
        type.getMethod("start").invoke(recorder);
      }
    } catch (Exception error) {
      if (acquired) ASYNC.set(false);
      Files.deleteIfExists(file);
      throw error;
    }
  }

  static String value(JsonObject p, String key, String defaultValue) {
    return p.has(key) && !p.get(key).getAsString().isEmpty()
        ? p.get(key).getAsString()
        : defaultValue;
  }

  private String execute(String command) throws Exception {
    return (String) type.getMethod("execute", String.class).invoke(recorder, command);
  }

  synchronized byte[] dump() throws Exception {
    if (closed) throw new IllegalStateException("Recording is closed");
    if (async) execute("dump,jfr,file=" + file.toString());
    else type.getMethod("dump", Path.class).invoke(recorder, file);
    long bytes = Files.size(file);
    if (bytes > 64L * 1024 * 1024)
      throw new IllegalStateException("Recording exceeds the 64 MiB protocol frame limit");
    return Files.readAllBytes(file);
  }

  public synchronized void close() throws Exception {
    if (closed) return;
    try {
      if (async) execute("stop");
      else type.getMethod("close").invoke(recorder);
    } finally {
      closed = true;
      if (async) ASYNC.set(false);
      Files.deleteIfExists(file);
    }
  }
}
