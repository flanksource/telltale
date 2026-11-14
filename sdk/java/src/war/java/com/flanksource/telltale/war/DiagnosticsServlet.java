package com.flanksource.telltale.war;

import java.io.*;
import java.lang.management.ManagementFactory;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;
import javax.management.*;
import javax.servlet.*;
import javax.servlet.http.*;

/**
 * Thin HTTP adapter: authentication and routing live in the JVM-wide runtime MBean, which this
 * servlet reaches only through JDK types because the runtime runs in an isolated classloader.
 */
public final class DiagnosticsServlet extends HttpServlet {
  private static final String RUNTIME = "com.flanksource.telltale:type=Diagnostics";
  /** Must equal RuntimeService.API_VERSION of the runtime JAR embedded in this WAR. */
  private static final int API_VERSION = 1;
  private static final String EMBEDDED_CONFIG = "/WEB-INF/telltale-diag.properties";
  private static final int MAX_REQUEST = 24 * 1024 * 1024;
  private static final Set<String> KEYS =
      new HashSet<>(
          Arrays.asList(
              "issuer", "audience", "jwks", "jwksFile", "jwksUrl", "configDigest", "runtimeDir",
              "extensionsDir", "allowMutation"));

  private final String owner = UUID.randomUUID().toString();
  private String configDigest = "";
  private Integer outdatedVersion;
  private boolean registered;

  public void init() throws ServletException {
    try {
      Properties properties = loadConfig();
      validate(properties);
      configDigest = properties.getProperty("configDigest", "");
      Map<String, Object> runtime = runtimeConfig(properties);
      if (!ManagementFactory.getPlatformMBeanServer().isRegistered(new ObjectName(RUNTIME)))
        bootstrap(properties, runtime);
      int version = runtimeApiVersion();
      if (version != API_VERSION) {
        outdatedVersion = version;
        getServletContext().log(outdatedMessage());
        return;
      }
      request("configure", runtime);
      request("registerAdapter", adapter(properties));
      registered = true;
    } catch (Exception error) {
      throw new ServletException("initialize Telltale diagnostic runtime: " + error.getMessage(), error);
    }
  }

  private Properties loadConfig() throws IOException {
    String file = getServletConfig().getInitParameter("configFile");
    if (file == null) file = getServletContext().getInitParameter("configFile");
    if (file == null) file = System.getProperty("telltale.diag.config");
    Properties properties = new Properties();
    try (InputStream embedded = getServletContext().getResourceAsStream(EMBEDDED_CONFIG)) {
      boolean external = file != null && !file.trim().isEmpty();
      if (embedded != null && external)
        throw new IllegalArgumentException(
            "ambiguous configuration: both " + EMBEDDED_CONFIG + " and configFile " + file);
      if (embedded != null) {
        properties.load(embedded);
        return properties;
      }
      if (!external)
        throw new IllegalArgumentException(
            "embed " + EMBEDDED_CONFIG + " or configure configFile / -Dtelltale.diag.config");
      if (!Paths.get(file).isAbsolute())
        throw new IllegalArgumentException("configFile must be absolute: " + file);
      try (InputStream input = Files.newInputStream(Paths.get(file))) {
        properties.load(input);
      }
      return properties;
    }
  }

  private static void validate(Properties properties) {
    if (properties.containsKey("tokenFile"))
      throw new IllegalArgumentException(
          "tokenFile is no longer supported; configure issuer, audience and jwks/jwksFile/jwksUrl");
    for (String key : properties.stringPropertyNames())
      if (!KEYS.contains(key)) throw new IllegalArgumentException("unknown configuration key " + key);
    required(properties, "issuer");
    required(properties, "audience");
    if (properties.containsKey("jwks") && properties.containsKey("jwksFile"))
      throw new IllegalArgumentException("jwks and jwksFile are mutually exclusive");
    if (!properties.containsKey("jwks")
        && !properties.containsKey("jwksFile")
        && !properties.containsKey("jwksUrl"))
      throw new IllegalArgumentException("configure jwks, jwksFile or jwksUrl");
    String allow = properties.getProperty("allowMutation", "false");
    if (!allow.equals("true") && !allow.equals("false"))
      throw new IllegalArgumentException("allowMutation must be true or false");
  }

  private static Map<String, Object> runtimeConfig(Properties properties) {
    Map<String, Object> config = new LinkedHashMap<>();
    config.put("allowMutation", Boolean.valueOf(properties.getProperty("allowMutation", "false")));
    config.put("extensionsDir", properties.getProperty("extensionsDir"));
    return config;
  }

  private Map<String, Object> adapter(Properties properties) throws IOException {
    Map<String, Object> adapter = new LinkedHashMap<>();
    adapter.put("owner", owner);
    adapter.put("issuer", properties.getProperty("issuer"));
    adapter.put("audience", properties.getProperty("audience"));
    String jwks = properties.getProperty("jwks");
    String jwksFile = properties.getProperty("jwksFile");
    if (jwksFile != null) {
      Path path = Paths.get(required(properties, "jwksFile"));
      if (!path.isAbsolute()) throw new IllegalArgumentException("jwksFile must be absolute");
      jwks = new String(Files.readAllBytes(path), StandardCharsets.UTF_8);
    }
    adapter.put("jwks", jwks);
    adapter.put("jwksUrl", properties.getProperty("jwksUrl"));
    return adapter;
  }

  private void bootstrap(Properties properties, Map<String, Object> config) throws Exception {
    Path directory = Paths.get(required(properties, "runtimeDir"));
    if (!directory.isAbsolute()) throw new IllegalArgumentException("runtimeDir must be absolute");
    byte[] bytes;
    try (InputStream input =
        getServletContext().getResourceAsStream("/WEB-INF/runtime/telltale-agent.jar")) {
      if (input == null) throw new IllegalStateException("diagnostic runtime JAR is missing from WAR");
      bytes = read(input, 64 * 1024 * 1024);
    }
    byte[] digest = MessageDigest.getInstance("SHA-256").digest(bytes);
    StringBuilder hex = new StringBuilder();
    for (byte value : digest) hex.append(String.format("%02x", value & 255));
    Files.createDirectories(directory);
    Path jar = directory.resolve("runtime-" + hex + ".jar");
    if (!Files.exists(jar)) Files.write(jar, bytes, StandardOpenOption.CREATE_NEW);
    if (!MessageDigest.isEqual(
        digest, MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(jar))))
      throw new IllegalStateException("staged runtime digest mismatch");
    URLClassLoader isolated =
        new URLClassLoader(
            new URL[] {jar.toUri().toURL()}, ClassLoader.getSystemClassLoader().getParent());
    isolated
        .loadClass("com.flanksource.telltale.RuntimeService")
        .getMethod("acquire", Map.class)
        .invoke(null, config);
  }

  private static int runtimeApiVersion() throws Exception {
    MBeanServer server = ManagementFactory.getPlatformMBeanServer();
    for (MBeanOperationInfo operation : server.getMBeanInfo(new ObjectName(RUNTIME)).getOperations())
      if (operation.getName().equals("describe")) {
        Object value =
            ((Map<?, ?>) request("describe", Collections.<String, Object>emptyMap()))
                .get("apiVersion");
        if (!(value instanceof Number))
          throw new IllegalStateException("diagnostic runtime describe returned no apiVersion");
        return ((Number) value).intValue();
      }
    return 0;
  }

  protected void service(HttpServletRequest request, HttpServletResponse response)
      throws IOException {
    String path = request.getPathInfo() == null ? "/" : request.getPathInfo();
    boolean health = "GET".equals(request.getMethod()) && "/health".equals(path);
    if (outdatedVersion != null) {
      json(response, 503, health ? outdatedHealth() : error(outdatedMessage()));
      return;
    }
    if (health) {
      json(
          response,
          200,
          "{\"status\":\"ok\",\"apiVersion\":" + API_VERSION + ",\"configDigest\":"
              + quote(configDigest) + "}");
      return;
    }
    String body;
    try {
      body = new String(read(request.getInputStream(), MAX_REQUEST), StandardCharsets.UTF_8);
    } catch (IOException error) {
      json(response, 400, error(error.getMessage()));
      return;
    }
    try {
      copy(response, (Map<?, ?>) request("http", envelope(request, path, body)));
    } catch (Exception error) {
      json(response, 500, error(error.toString()));
    }
  }

  private Map<String, Object> envelope(HttpServletRequest request, String path, String body) {
    Map<String, Object> envelope = new LinkedHashMap<>();
    envelope.put("method", request.getMethod());
    envelope.put("path", path);
    envelope.put("owner", owner);
    envelope.put("body", body);
    envelope.put("authorization", request.getHeader("Authorization"));
    // The apiserver strips Authorization from proxied requests; tokens through it use this
    // header (JwtVerifier.HEADER, which this servlet's loader cannot see).
    envelope.put("telltaleAuthorization", request.getHeader("X-Telltale-Authorization"));
    return envelope;
  }

  private static void copy(HttpServletResponse response, Map<?, ?> reply) throws IOException {
    response.setStatus((Integer) reply.get("status"));
    for (Map.Entry<?, ?> header : ((Map<?, ?>) reply.get("headers")).entrySet())
      response.setHeader((String) header.getKey(), (String) header.getValue());
    String contentType = (String) reply.get("contentType");
    if (contentType != null && !contentType.isEmpty()) response.setContentType(contentType);
    byte[] body = (byte[]) reply.get("body");
    if (body.length > 0) response.getOutputStream().write(body);
  }

  private String outdatedMessage() {
    return "diagnostic runtime API "
        + outdatedVersion
        + " is loaded in this JVM; this adapter requires "
        + API_VERSION
        + "; restart the JVM to replace it";
  }

  private String outdatedHealth() {
    return "{\"status\":\"outdated\",\"apiVersion\":" + outdatedVersion + ",\"required\":"
        + API_VERSION + ",\"error\":" + quote(outdatedMessage()) + "}";
  }

  private static String error(String message) {
    return "{\"error\":" + quote(message == null ? "" : message) + "}";
  }

  private static void json(HttpServletResponse response, int status, String body)
      throws IOException {
    response.setStatus(status);
    response.setContentType("application/json; charset=UTF-8");
    response.getOutputStream().write(body.getBytes(StandardCharsets.UTF_8));
  }

  private static String quote(String value) {
    StringBuilder result = new StringBuilder("\"");
    for (char c : value.toCharArray()) {
      if (c == '"' || c == '\\') result.append('\\').append(c);
      else if (c < 0x20) result.append(String.format("\\u%04x", (int) c));
      else result.append(c);
    }
    return result.append('"').toString();
  }

  public void destroy() {
    if (!registered) return;
    try {
      request("closeOwner", Collections.<String, Object>singletonMap("owner", owner));
    } catch (Exception error) {
      getServletContext().log("Telltale capture cleanup failed", error);
    }
  }

  private static Object request(String action, Map<String, Object> request) throws Exception {
    try {
      return ManagementFactory.getPlatformMBeanServer()
          .invoke(
              new ObjectName(RUNTIME),
              action,
              new Object[] {request},
              new String[] {Map.class.getName()});
    } catch (MBeanException error) {
      throw error.getTargetException();
    }
  }

  private static String required(Properties values, String key) {
    String value = values.getProperty(key);
    if (value == null || value.trim().isEmpty())
      throw new IllegalArgumentException(key + " is required");
    return value;
  }

  private static byte[] read(InputStream input, int maximum) throws IOException {
    ByteArrayOutputStream result = new ByteArrayOutputStream();
    byte[] buffer = new byte[8192];
    int count;
    while ((count = input.read(buffer)) != -1) {
      if (count > maximum - result.size())
        throw new IOException("request or artifact exceeds size limit");
      result.write(buffer, 0, count);
    }
    return result.toByteArray();
  }
}
