package com.flanksource.telltale;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import java.lang.instrument.Instrumentation;
import java.lang.management.ManagementFactory;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Instant;
import java.util.UUID;

public final class Agent {
  static final Gson JSON = new Gson();
  private static Agent instance;
  final JsonObject config;
  final String pid = ManagementFactory.getRuntimeMXBean().getName().split("@")[0];
  final String started = Instant.now().toString();
  final String remoteID = "java/" + UUID.randomUUID();
  private final ServerSocket listener;

  private Agent(JsonObject options) throws Exception {
    config = options;
    if (!config.has("token") || config.get("token").getAsString().length() < 16)
      throw new IllegalArgumentException("Agent token is required");
    listener = new ServerSocket(0, 16, InetAddress.getByName("127.0.0.1"));
    if (config.has("discovery")) {
      JsonObject endpoint = new JsonObject();
      endpoint.addProperty("pid", Integer.parseInt(pid));
      endpoint.addProperty(
          "addr", listener.getInetAddress().getHostAddress() + ":" + listener.getLocalPort());
      endpoint.addProperty("token", config.get("token").getAsString());
      endpoint.addProperty("started", started);
      Path path = Paths.get(config.get("discovery").getAsString());
      Files.createDirectories(path.getParent());
      Files.setPosixFilePermissions(
          path.getParent(), java.nio.file.attribute.PosixFilePermissions.fromString("rwx------"));
      Path staged =
          Files.createTempFile(
              path.getParent(),
              "endpoint-",
              ".stage",
              java.nio.file.attribute.PosixFilePermissions.asFileAttribute(
                  java.nio.file.attribute.PosixFilePermissions.fromString("rw-------")));
      Files.write(staged, JSON.toJson(endpoint).getBytes(StandardCharsets.UTF_8));
      Files.move(
          staged,
          path,
          java.nio.file.StandardCopyOption.REPLACE_EXISTING,
          java.nio.file.StandardCopyOption.ATOMIC_MOVE);
      path.toFile().deleteOnExit();
    }
    Thread accept = new Thread(() -> accept(), "telltale-agent");
    accept.setDaemon(true);
    accept.start();
    if (config.has("url")) Hub.start(this, config.get("url").getAsString());
  }

  private void accept() {
    while (!listener.isClosed()) {
      try {
        Socket socket = listener.accept();
        socket.setSoTimeout(30000);
        Thread thread =
            new Thread(
                () -> {
                  try (Socket connection = socket;
                      Wire wire = new Wire(connection)) {
                    new Session(this, wire, false).run();
                  } catch (Exception error) {
                    System.err.println("telltale local agent: " + error.getClass().getSimpleName());
                  }
                },
                "telltale-session");
        thread.setDaemon(true);
        thread.start();
      } catch (Exception error) {
        System.err.println("telltale accept: " + error.getClass().getSimpleName());
        return;
      }
    }
  }

  public static synchronized void agentmain(String args, Instrumentation instrumentation)
      throws Exception {
    start(args);
  }

  public static synchronized void premain(String args, Instrumentation instrumentation)
      throws Exception {
    start(args);
  }

  private static void start(String path) throws Exception {
    JsonObject config =
        JSON.fromJson(
            new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8),
            JsonObject.class);
    if (instance != null) {
      if (!instance.config.equals(config))
        throw new IllegalStateException(
            "Telltale agent already installed with different configuration");
      return;
    }
    instance = new Agent(config);
  }

  JsonObject hello(boolean remote) {
    JsonObject hello = new JsonObject();
    hello.addProperty("agentId", remote ? remoteID : pid);
    hello.addProperty("protocol", 1);
    hello.addProperty("started", started);
    hello.addProperty("service", Recording.value(config, "service", "java"));
    hello.addProperty("sdkVersion", Agent.class.getPackage().getImplementationVersion());
    JsonObject runtime = new JsonObject();
    runtime.addProperty("lang", "java");
    runtime.addProperty("version", System.getProperty("java.version"));
    runtime.addProperty("vendor", System.getProperty("java.vendor"));
    hello.add("runtime", runtime);
    JsonObject resource =
        config.has("resource") ? config.getAsJsonObject("resource").deepCopy() : new JsonObject();
    resource.addProperty("process.pid", pid);
    resource.addProperty("service.name", Recording.value(config, "service", "java"));
    hello.add("resource", resource);
    JsonObject probe = new JsonObject();
    probe.addProperty("name", "jvm_jfr");
    probe.addProperty("title", "JVM JFR");
    probe.addProperty("checkpoints", true);
    com.google.gson.JsonArray probes = new com.google.gson.JsonArray();
    probes.add(probe);
    hello.add("probes", probes);
    return hello;
  }
}
