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

public final class DiagnosticsServlet extends HttpServlet {
  private static final String RUNTIME = "com.flanksource.telltale:type=Diagnostics";
  private byte[] token;
  private final String owner = UUID.randomUUID().toString();

  public void init() throws ServletException {
    try {
      String file = getServletConfig().getInitParameter("configFile");
      if (file == null) file = getServletContext().getInitParameter("configFile");
      if (file == null) file = System.getProperty("telltale.diag.config");
      if (file == null || file.trim().isEmpty())
        throw new IllegalArgumentException(
            "configure configFile or -Dtelltale.diag.config with an absolute properties file");
      Properties properties = new Properties();
      try (InputStream input = Files.newInputStream(Paths.get(file))) {
        properties.load(input);
      }
      String tokenFile = required(properties, "tokenFile");
      String secret =
          new String(Files.readAllBytes(Paths.get(tokenFile)), StandardCharsets.UTF_8).trim();
      if (secret.length() < 16)
        throw new IllegalArgumentException(
            "diagnostic token must contain at least sixteen characters");
      token = secret.getBytes(StandardCharsets.UTF_8);
      Map<String, Object> config = new LinkedHashMap<>();
      for (String key : properties.stringPropertyNames())
        if (!key.equals("tokenFile") && !key.equals("runtimeDir") && !key.equals("allowMutation"))
          config.put(key, properties.getProperty(key));
      String allow = properties.getProperty("allowMutation", "false");
      if (!allow.equals("true") && !allow.equals("false"))
        throw new IllegalArgumentException("allowMutation must be true or false");
      config.put("allowMutation", Boolean.valueOf(allow));
      config.put("token", secret);
      MBeanServer server = ManagementFactory.getPlatformMBeanServer();
      if (server.isRegistered(new ObjectName(RUNTIME))) {
        Map<String, Object> identity = new LinkedHashMap<>();
        identity.put("allowMutation", config.get("allowMutation"));
        identity.put("extensionsDir", config.get("extensionsDir"));
        request("configure", identity);
      } else {
        Path directory = Paths.get(required(properties, "runtimeDir"));
        if (!directory.isAbsolute())
          throw new IllegalArgumentException("runtimeDir must be absolute");
        byte[] bytes;
        try (InputStream input =
            getServletContext().getResourceAsStream("/WEB-INF/runtime/telltale-agent.jar")) {
          if (input == null)
            throw new IllegalStateException("diagnostic runtime JAR is missing from WAR");
          bytes = read(input, 64 * 1024 * 1024);
        }
        StringBuilder digest = new StringBuilder();
        for (byte value : MessageDigest.getInstance("SHA-256").digest(bytes))
          digest.append(String.format("%02x", value & 255));
        Files.createDirectories(directory);
        Path jar = directory.resolve("runtime-" + digest + ".jar");
        if (!Files.exists(jar)) Files.write(jar, bytes, StandardOpenOption.CREATE_NEW);
        if (!MessageDigest.isEqual(
            MessageDigest.getInstance("SHA-256").digest(bytes),
            MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(jar))))
          throw new IllegalStateException("staged runtime digest mismatch");
        URLClassLoader isolated =
            new URLClassLoader(
                new URL[] {jar.toUri().toURL()}, ClassLoader.getSystemClassLoader().getParent());
        isolated
            .loadClass("com.flanksource.telltale.Agent")
            .getMethod("start", Map.class)
            .invoke(null, config);
      }
    } catch (Exception error) {
      throw new ServletException("initialize Telltale diagnostic runtime", error);
    }
  }

  protected void service(HttpServletRequest request, HttpServletResponse response)
      throws IOException {
    String path = request.getPathInfo();
    if ("GET".equals(request.getMethod()) && "/health".equals(path)) {
      response.setContentType("application/json");
      response.getWriter().write("{\"status\":\"ok\"}");
      return;
    }
    String authorization = request.getHeader("Authorization");
    byte[] supplied =
        authorization != null && authorization.startsWith("Bearer ")
            ? authorization.substring(7).getBytes(StandardCharsets.UTF_8)
            : new byte[0];
    if (token == null || !MessageDigest.isEqual(token, supplied)) {
      response.sendError(401, "invalid diagnostic token");
      return;
    }
    try {
      Map<String, Object> envelope = new LinkedHashMap<>();
      envelope.put("method", request.getMethod());
      envelope.put("path", path);
      envelope.put("owner", owner);
      envelope.put(
          "body",
          new String(read(request.getInputStream(), 24 * 1024 * 1024), StandardCharsets.UTF_8));
      Object result = request("http", envelope);
      if (result instanceof Map) {
        Map<?, ?> artifact = (Map<?, ?>) result;
        response.setContentType((String) artifact.get("contentType"));
        response.setHeader(
            "Content-Disposition", "attachment; filename=\"" + artifact.get("fileName") + "\"");
        response.getOutputStream().write((byte[]) artifact.get("data"));
      } else {
        response.setContentType("application/json; charset=UTF-8");
        response.getWriter().write((String) result);
      }
    } catch (Exception error) {
      response.sendError(400, error.getMessage());
    }
  }

  public void destroy() {
    try {
      request("closeOwner", Collections.singletonMap("owner", owner));
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
