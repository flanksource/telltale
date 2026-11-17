package com.flanksource.telltale;

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
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
  static final Gson JSON = new GsonBuilder().serializeNulls().create();
  private static Agent instance;
  final JsonObject config;
  final RuntimeClient diagnostics;
  final String pid = ManagementFactory.getRuntimeMXBean().getName().split("@")[0];
  final String started = Instant.now().toString();
  final String remoteID = "java/" + UUID.randomUUID();
  private final ServerSocket listener;
  private HttpListener http;
  private final java.util.Set<Socket> clients = java.util.Collections.synchronizedSet(new java.util.HashSet<Socket>());
  private final java.util.Set<Thread> sessions = new java.util.HashSet<>();
  final java.util.concurrent.atomic.AtomicReference<Exception> stopFailure = new java.util.concurrent.atomic.AtomicReference<>();
  volatile boolean closed;
  volatile Hub hub;
  private Thread hubThread;

  private Agent(JsonObject options) throws Exception {
    config = options;
    if (!config.has("token") || config.get("token").getAsString().length() < 16)
      throw new IllegalArgumentException("Agent token is required");
    diagnostics = new RuntimeClient(JSON.fromJson(options, java.util.Map.class));
    com.flanksource.telltale.observer.ObserverRuntime.install(com.flanksource.telltale.diag.hotpatch.InstrumentationService.instrumentation());
    listener = new ServerSocket(0, 16, InetAddress.getByName("127.0.0.1"));
    try {
      if (config.has("http")) http = HttpListener.start(JSON.fromJson(config.get("http"), java.util.Map.class));
      publishDiscovery();
      Thread accept = new Thread(() -> accept(), "telltale-agent");
      accept.setDaemon(true);
      accept.start();
      if (config.has("url")) hubThread = Hub.start(this, config.get("url").getAsString());
    } catch (Exception | LinkageError failure) {
      try { closeTransports(); }
      catch (Exception cleanup) { failure.addSuppressed(cleanup); }
      throw failure;
    }
  }

  private void publishDiscovery() throws Exception {
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
  }

  private void accept() {
    while (!listener.isClosed()) {
      try {
        Socket socket = listener.accept();
        socket.setSoTimeout(30000);
        Thread thread =
            new Thread(
                () -> {
                  Session session = null;
                  try (Socket connection = socket;
                      Wire wire = new Wire(connection)) {
                    session = new Session(this, wire, false);
                    session.run();
                  } catch (Exception error) {
                    if (!closed) System.err.println("telltale local agent: " + error.getClass().getSimpleName());
                    else if (session != null && !session.stopped()) stopFailure.compareAndSet(null, error);
                  } finally {
                    synchronized (clients) {
                      clients.remove(socket);
                      sessions.remove(Thread.currentThread());
                    }
                  }
                },
                "telltale-session");
        thread.setDaemon(true);
        synchronized (clients) {
          if (closed) { socket.close(); return; }
          clients.add(socket);
          sessions.add(thread);
          thread.start();
        }
      } catch (Exception error) {
        if (!closed) System.err.println("telltale accept: " + error.getClass().getSimpleName());
        return;
      }
    }
  }

  public static synchronized void agentmain(String args, Instrumentation instrumentation)
      throws Exception {
    com.flanksource.telltale.diag.agent.DiagAgent.agentmain(args, instrumentation);
    System.setProperty("telltale.runtime.dir", Paths.get(args).toAbsolutePath().getParent().toString());
    start(
        JSON.fromJson(
            new String(Files.readAllBytes(Paths.get(args)), StandardCharsets.UTF_8),
            java.util.Map.class));
    com.flanksource.telltale.observer.ObserverRuntime.install(instrumentation);
  }

  public static synchronized void premain(String args, Instrumentation instrumentation)
      throws Exception {
    com.flanksource.telltale.diag.agent.DiagAgent.premain(args, instrumentation);
    System.setProperty("telltale.runtime.dir", Paths.get(args).toAbsolutePath().getParent().toString());
    start(
        JSON.fromJson(
            new String(Files.readAllBytes(Paths.get(args)), StandardCharsets.UTF_8),
            java.util.Map.class));
    com.flanksource.telltale.observer.ObserverRuntime.install(instrumentation);
  }

  @SuppressWarnings("unchecked")
  public static void validateRuntime(String path) throws Exception {
    JsonObject config = JSON.fromJson(new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8), JsonObject.class);
    if (config == null || !config.has("token") || config.get("token").getAsString().length() < 16)
      throw new IllegalArgumentException("Agent token is required");
    AgentResource.from(JSON.fromJson(config, java.util.Map.class));
    if (config.has("http")) HttpListener.validate(JSON.fromJson(config.get("http"), java.util.Map.class));
    int kernel = com.flanksource.telltale.diag.agent.DiagAgent.probeApiVersion();
    if (kernel != com.flanksource.telltale.diag.agent.DiagAgent.PROBE_API_VERSION)
      throw new IllegalStateException("incompatible probe bridge API " + kernel + "; restart the JVM to update the bootstrap bridge");
  }

  public static void prepareUpgrade() throws Exception {
    RuntimeClient.request("prepareUpgrade", java.util.Collections.emptyMap());
  }

  public static synchronized void stopRuntime() throws Exception {
    if (instance == null) {
      javax.management.MBeanServer server = ManagementFactory.getPlatformMBeanServer();
      if (server.isRegistered(new javax.management.ObjectName(RuntimeService.NAME))) {
        prepareUpgrade();
        RuntimeClient.request("stopRuntime", java.util.Collections.emptyMap());
      }
      return;
    }
    instance.closeTransports();
    RuntimeClient.request("stopRuntime", java.util.Collections.emptyMap());
    instance = null;
  }

  private void closeTransports() throws Exception {
    closed = true;
    listener.close();
    java.util.List<Thread> stopping;
    synchronized (clients) {
      for (Socket client : clients) client.close();
      stopping = new java.util.ArrayList<>(sessions);
    }
    long deadline = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(6);
    for (Thread session : stopping) {
      session.join(Math.max(1, java.util.concurrent.TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime())));
      if (session.isAlive()) throw new IllegalStateException("agent session did not stop for upgrade");
    }
    if (stopFailure.get() != null) throw new IllegalStateException("agent session cleanup failed", stopFailure.get());
    if (hub != null) hub.close();
    if (hubThread != null) {
      hubThread.interrupt();
      hubThread.join(5000);
      if (hubThread.isAlive()) throw new IllegalStateException("hub did not stop for upgrade");
    }
    if (stopFailure.get() != null) throw new IllegalStateException("hub session cleanup failed", stopFailure.get());
    if (http != null) http.close();
  }

  public static synchronized void start(java.util.Map<String, Object> options) throws Exception {
    JsonObject config = JSON.toJsonTree(options).getAsJsonObject();
    if (instance != null) {
      if (!instance.config.equals(config))
        throw new IllegalStateException(
            "Telltale agent already installed with different configuration");
      return;
    }
    instance = new Agent(config);
  }

  JsonObject hello(boolean remote) throws Exception {
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
    hello.add("resource", JSON.toJsonTree(AgentResource.from(JSON.fromJson(config, java.util.Map.class))));
    hello.add("probes", JSON.toJsonTree(RuntimeService.probes()));
    hello.add("ops", JSON.toJsonTree(diagnostics.catalog()));
    return hello;
  }
}
