package com.flanksource.telltale;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;

/** The attached agent's HTTP adapter: the WAR's routes for JVMs that have no servlet container. */
final class HttpListener implements AutoCloseable {
  private static final int MAX_REQUEST = 24 * 1024 * 1024;
  private static final Set<String> KEYS =
      new HashSet<>(
          Arrays.asList(
              "bind", "port", "portFile", "issuer", "audience", "jwks", "jwksUrl", "configDigest"));

  private final String owner = UUID.randomUUID().toString();
  private final String configDigest;
  private final int runtimeApiVersion;
  private final ThreadPoolExecutor executor =
      new ThreadPoolExecutor(
          4,
          4,
          0,
          TimeUnit.SECONDS,
          new ArrayBlockingQueue<Runnable>(64),
          runnable -> {
            Thread thread = new Thread(runnable, "telltale-http");
            thread.setDaemon(true);
            return thread;
          },
          new ThreadPoolExecutor.CallerRunsPolicy());
  private final HttpServer server;
  private boolean registered;
  private boolean started;

  private HttpListener(Map<String, Object> config) throws Exception {
    for (String key : config.keySet())
      if (!KEYS.contains(key)) throw new IllegalArgumentException("unknown http option " + key);
    configDigest = Diagnostics.text(config, "configDigest", "");
    Path portFile = portFile(config);
    runtimeApiVersion = RuntimeClient.apiVersion();
    server = HttpServer.create(address(config), 16);
    try {
      if (runtimeApiVersion == RuntimeService.API_VERSION) register(config);
      server.setExecutor(executor);
      server.createContext("/", this::exchange);
      startDaemon();
      if (portFile != null) writePort(portFile, server.getAddress().getPort());
    } catch (Exception error) {
      try {
        close();
      } catch (Exception cleanup) {
        error.addSuppressed(cleanup);
      }
      throw error;
    }
  }

  static HttpListener start(Map<String, Object> config) throws Exception {
    return new HttpListener(config);
  }

  int port() {
    return server.getAddress().getPort();
  }

  private void register(Map<String, Object> config) throws Exception {
    Map<String, Object> request = new LinkedHashMap<>();
    request.put("owner", owner);
    request.put("issuer", config.get("issuer"));
    request.put("audience", config.get("audience"));
    request.put("jwks", config.get("jwks"));
    request.put("jwksUrl", config.get("jwksUrl"));
    RuntimeClient.request("registerAdapter", request);
    registered = true;
  }

  private static InetSocketAddress address(Map<String, Object> config) throws IOException {
    String bind = Diagnostics.required(config, "bind");
    Object port = config.get("port");
    if (!(port instanceof Number)) throw new IllegalArgumentException("http port is required");
    double value = ((Number) port).doubleValue();
    if (value != Math.rint(value) || value < 0 || value > 65535)
      throw new IllegalArgumentException("http port must be an integer between 0 and 65535");
    return new InetSocketAddress(InetAddress.getByName(bind), (int) value);
  }

  /** The dispatcher thread inherits daemon status, so it must not keep the target JVM alive. */
  private void startDaemon() throws Exception {
    FutureTask<Void> start = new FutureTask<>(server::start, null);
    Thread starter = new Thread(start, "telltale-http-start");
    starter.setDaemon(true);
    starter.start();
    try {
      start.get();
    } catch (ExecutionException error) {
      throw new IllegalStateException("start diagnostic HTTP listener", error.getCause());
    }
    started = true;
  }

  private static Path portFile(Map<String, Object> config) {
    String file = Diagnostics.text(config, "portFile", null);
    if (file == null) return null;
    Path path = Paths.get(file);
    if (!path.isAbsolute()) throw new IllegalArgumentException("http portFile must be absolute");
    return path;
  }

  private static void writePort(Path path, int port) throws IOException {
    Files.createDirectories(path.getParent());
    Path staged = Files.createTempFile(path.getParent(), "port-", ".stage");
    Files.write(staged, Integer.toString(port).getBytes(StandardCharsets.UTF_8));
    Files.move(staged, path, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
  }

  private void exchange(HttpExchange exchange) throws IOException {
    try {
      respond(exchange, reply(exchange));
    } catch (IllegalArgumentException error) {
      respond(exchange, RuntimeHttp.error(400, error.getMessage()));
    } catch (Exception error) {
      respond(exchange, RuntimeHttp.error(500, error.toString()));
    } finally {
      exchange.close();
    }
  }

  private Map<String, Object> reply(HttpExchange exchange) throws Exception {
    String method = exchange.getRequestMethod(), path = exchange.getRequestURI().getPath();
    boolean health = method.equals("GET") && path.equals("/health");
    if (runtimeApiVersion != RuntimeService.API_VERSION)
      return health ? outdatedHealth() : RuntimeHttp.error(503, outdatedMessage());
    if (health) return health();
    Map<String, Object> envelope = new LinkedHashMap<>();
    envelope.put("method", method);
    envelope.put("path", path);
    envelope.put("owner", owner);
    envelope.put("body", new String(read(exchange.getRequestBody()), StandardCharsets.UTF_8));
    envelope.put("authorization", exchange.getRequestHeaders().getFirst("Authorization"));
    envelope.put(
        "telltaleAuthorization",
        exchange.getRequestHeaders().getFirst(com.flanksource.telltale.auth.JwtVerifier.HEADER));
    @SuppressWarnings("unchecked")
    Map<String, Object> reply = (Map<String, Object>) RuntimeClient.request("http", envelope);
    return reply;
  }

  private Map<String, Object> health() {
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("status", "ok");
    body.put("apiVersion", RuntimeService.API_VERSION);
    body.put("configDigest", configDigest);
    return jsonReply(200, body);
  }

  private Map<String, Object> outdatedHealth() {
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("status", "outdated");
    body.put("apiVersion", runtimeApiVersion);
    body.put("required", RuntimeService.API_VERSION);
    body.put("error", outdatedMessage());
    return jsonReply(503, body);
  }

  private String outdatedMessage() {
    return "diagnostic runtime API "
        + runtimeApiVersion
        + " is loaded in this JVM; this adapter requires "
        + RuntimeService.API_VERSION
        + "; restart the JVM to replace it";
  }

  private static Map<String, Object> jsonReply(int status, Object body) {
    Map<String, Object> reply = new LinkedHashMap<>();
    reply.put("status", status);
    reply.put("contentType", RuntimeHttp.JSON_TYPE);
    reply.put("body", Agent.JSON.toJson(body).getBytes(StandardCharsets.UTF_8));
    reply.put("headers", Collections.emptyMap());
    return reply;
  }

  private static void respond(HttpExchange exchange, Map<String, Object> reply) throws IOException {
    byte[] body = (byte[]) reply.get("body");
    String contentType = (String) reply.get("contentType");
    if (contentType != null && !contentType.isEmpty())
      exchange.getResponseHeaders().set("Content-Type", contentType);
    for (Map.Entry<?, ?> header : ((Map<?, ?>) reply.get("headers")).entrySet())
      exchange.getResponseHeaders().set((String) header.getKey(), (String) header.getValue());
    exchange.sendResponseHeaders((Integer) reply.get("status"), body.length == 0 ? -1 : body.length);
    if (body.length > 0)
      try (OutputStream output = exchange.getResponseBody()) {
        output.write(body);
      }
  }

  private static byte[] read(InputStream input) throws IOException {
    ByteArrayOutputStream result = new ByteArrayOutputStream();
    byte[] buffer = new byte[8192];
    int count;
    while ((count = input.read(buffer)) != -1) {
      if (count > MAX_REQUEST - result.size())
        throw new IllegalArgumentException("request exceeds 24 MiB size limit");
      result.write(buffer, 0, count);
    }
    return result.toByteArray();
  }

  public void close() throws Exception {
    if (started) server.stop(0);
    executor.shutdownNow();
    if (registered)
      RuntimeClient.request("closeOwner", Collections.singletonMap("owner", owner));
  }
}
