package com.flanksource.telltale;

import java.lang.management.ManagementFactory;
import java.util.*;
import javax.management.*;

/** One JVM-wide owner, reached across application and servlet classloaders using JDK types. */
public final class RuntimeService implements DynamicMBean {
  public static final String NAME = "com.flanksource.telltale:type=Diagnostics";
  private final Diagnostics diagnostics;
  private final Map<String, Object> configuration;
  private final Map<String, Capture> captures = new HashMap<>();
  private final Map<String, String> owners = new HashMap<>();

  private RuntimeService(Map<String, Object> config) throws Exception {
    configuration = configuration(config);
    diagnostics = new Diagnostics(config);
  }

  public static void acquire(Map<String, Object> config) throws Exception {
    MBeanServer server = ManagementFactory.getPlatformMBeanServer();
    ObjectName name = new ObjectName(NAME);
    synchronized (server) {
      if (!server.isRegistered(name)) server.registerMBean(new RuntimeService(config), name);
    }
    server.invoke(
        name,
        "configure",
        new Object[] {configuration(config)},
        new String[] {Map.class.getName()});
  }

  private static Map<String, Object> configuration(Map<String, Object> config) {
    Map<String, Object> result = new LinkedHashMap<>();
    result.put("allowMutation", Boolean.TRUE.equals(config.get("allowMutation")));
    result.put("extensionsDir", config.get("extensionsDir"));
    return result;
  }

  @SuppressWarnings("unchecked")
  public Object invoke(String action, Object[] values, String[] signature)
      throws MBeanException, ReflectionException {
    try {
      Map<String, Object> request = (Map<String, Object>) values[0];
      switch (action) {
        case "configure":
          if (!configuration.equals(request))
            throw new IllegalStateException(
                "diagnostic runtime already initialized with different mutation or extension"
                    + " configuration");
          return null;
        case "catalog":
          return diagnostics.catalog();
        case "http":
          return http(request);
        case "closeOwner":
          {
            List<String> ids = new ArrayList<>();
            synchronized (captures) {
              for (Map.Entry<String, String> entry : owners.entrySet())
                if (entry.getValue().equals(request.get("owner"))) ids.add(entry.getKey());
            }
            Exception failure = null;
            for (String id : ids)
              try {
                invoke(
                    "captureClose", new Object[] {Collections.singletonMap("id", id)}, signature);
              } catch (Exception error) {
                if (failure == null) failure = error;
                else failure.addSuppressed(error);
              }
            if (failure != null) throw failure;
            return null;
          }
        case "call":
          return diagnostics.call(
              Diagnostics.required(request, "operation"),
              (Map<String, Object>) request.get("params"),
              Boolean.TRUE.equals(request.get("allowMutation")));
        case "captureOpen":
          {
            String key = Diagnostics.required(request, "id"),
                kind = Diagnostics.required(request, "kind");
            synchronized (captures) {
              if (captures.containsKey(key))
                throw new IllegalArgumentException("duplicate capture " + key);
              Capture capture =
                  kind.equals("jvm_jfr")
                      ? new Recording(
                          Agent.JSON.toJsonTree(request.get("params")).getAsJsonObject())
                      : diagnostics.capture(
                          kind,
                          (Map<String, Object>) request.get("params"),
                          key,
                          Boolean.TRUE.equals(request.get("allowMutation")));
              captures.put(key, capture);
              if (request.containsKey("owner")) owners.put(key, (String) request.get("owner"));
              Map<String, Object> result = new LinkedHashMap<>();
              result.put("id", key);
              result.put("streaming", capture.streaming());
              result.put("contentType", capture.contentType());
              result.put("fileName", capture.fileName());
              return result;
            }
          }
        default:
          {
            String key = Diagnostics.required(request, "id");
            Capture capture;
            synchronized (captures) {
              capture = captures.get(key);
            }
            if (capture == null) throw new IllegalArgumentException("unknown capture " + key);
            synchronized (capture) {
              switch (action) {
                case "captureDump":
                  return capture.dump();
                case "captureComplete":
                  return capture.complete();
                case "captureFinish":
                  return capture.finish();
                case "captureRenew":
                  capture.renew();
                  return null;
                case "captureClose":
                  capture.close();
                  synchronized (captures) {
                    captures.remove(key);
                    owners.remove(key);
                  }
                  return null;
                default:
                  throw new IllegalArgumentException("unknown runtime action " + action);
              }
            }
          }
      }
    } catch (Exception error) {
      throw new MBeanException(error, error.toString());
    }
  }

  @SuppressWarnings("unchecked")
  private Object http(Map<String, Object> envelope) throws Exception {
    String path = Diagnostics.required(envelope, "path"),
        method = Diagnostics.required(envelope, "method");
    String body = (String) envelope.get("body");
    Map<String, Object> params =
        body == null || body.trim().isEmpty()
            ? new LinkedHashMap<>()
            : com.flanksource.telltale.diag.Json.read(
                new java.io.ByteArrayInputStream(
                    body.getBytes(java.nio.charset.StandardCharsets.UTF_8)),
                Map.class);
    if (params == null) throw new IllegalArgumentException("request body must be an object");
    if (path.equals("/catalog") && method.equals("GET"))
      return Agent.JSON.toJson(diagnostics.catalog());
    if (path.equals("/call") && method.equals("POST"))
      return Agent.JSON.toJson(
          invoke("call", new Object[] {params}, new String[] {Map.class.getName()}));
    if (path.equals("/captures") && method.equals("POST")) {
      params.put("id", UUID.randomUUID().toString());
      params.put("owner", envelope.get("owner"));
      return Agent.JSON.toJson(
          invoke("captureOpen", new Object[] {params}, new String[] {Map.class.getName()}));
    }
    if (path.startsWith("/captures/")) {
      String id = path.substring("/captures/".length());
      Capture capture;
      synchronized (captures) {
        capture = captures.get(id);
        if (!Objects.equals(owners.get(id), envelope.get("owner")))
          throw new IllegalArgumentException("capture does not belong to this adapter");
      }
      if (capture == null) throw new IllegalArgumentException("unknown capture " + id);
      Map<String, Object> result = new LinkedHashMap<>();
      result.put("contentType", capture.contentType());
      result.put("fileName", capture.fileName());
      if (method.equals("POST"))
        result.put(
            "data",
            invoke(
                "captureDump",
                new Object[] {Collections.singletonMap("id", id)},
                new String[] {Map.class.getName()}));
      else if (method.equals("DELETE")) {
        try {
          result.put(
              "data",
              invoke(
                  "captureFinish",
                  new Object[] {Collections.singletonMap("id", id)},
                  new String[] {Map.class.getName()}));
        } finally {
          invoke(
              "captureClose",
              new Object[] {Collections.singletonMap("id", id)},
              new String[] {Map.class.getName()});
        }
      } else throw new IllegalArgumentException("unsupported capture method " + method);
      return result;
    }
    throw new IllegalArgumentException("unknown diagnostic route " + path);
  }

  public Object getAttribute(String name) throws AttributeNotFoundException {
    throw new AttributeNotFoundException(name);
  }

  public void setAttribute(Attribute attribute) throws AttributeNotFoundException {
    throw new AttributeNotFoundException(attribute.getName());
  }

  public AttributeList getAttributes(String[] names) {
    return new AttributeList();
  }

  public AttributeList setAttributes(AttributeList values) {
    return new AttributeList();
  }

  public MBeanInfo getMBeanInfo() {
    List<MBeanOperationInfo> operations = new ArrayList<>();
    for (String action :
        new String[] {
          "configure",
          "catalog",
          "call",
          "http",
          "closeOwner",
          "captureOpen",
          "captureDump",
          "captureComplete",
          "captureFinish",
          "captureRenew",
          "captureClose"
        })
      operations.add(
          new MBeanOperationInfo(
              action,
              action,
              new MBeanParameterInfo[] {
                new MBeanParameterInfo("request", Map.class.getName(), "Request")
              },
              Object.class.getName(),
              MBeanOperationInfo.ACTION));
    return new MBeanInfo(
        getClass().getName(),
        "Telltale diagnostic runtime",
        null,
        null,
        operations.toArray(new MBeanOperationInfo[0]),
        null);
  }
}
