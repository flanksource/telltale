package com.flanksource.telltale;

import com.flanksource.telltale.auth.JwtVerifier;
import java.lang.management.ManagementFactory;
import java.time.Instant;
import java.util.*;
import javax.management.*;

/** One JVM-wide owner, reached across application and servlet classloaders using JDK types. */
public final class RuntimeService implements DynamicMBean {
  public static final String NAME = "com.flanksource.telltale:type=Diagnostics";
  /** Bumped whenever an adapter-visible MBean action or its envelope changes. */
  public static final int API_VERSION = 1;

  private static final String[] ACTIONS = {
    "configure", "describe", "catalog", "call", "http", "registerAdapter", "closeOwner",
    "captureOpen", "captureDump", "captureComplete", "captureFinish", "captureRenew", "captureClose"
  };

  final Diagnostics diagnostics;
  final String started = Instant.now().toString();
  private final Map<String, Object> configuration;
  private final Map<String, Capture> captures = new HashMap<>();
  private final Map<String, String> owners = new HashMap<>();
  private final Map<String, Scope> captureScopes = new HashMap<>();
  private final Map<String, JwtVerifier> verifiers = new HashMap<>();
  private final RuntimeHttp http = new RuntimeHttp(this);

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

  /** The probe kinds every adapter advertises, with the scope each capture requires. */
  static List<Map<String, Object>> probes() {
    List<Map<String, Object>> probes = new ArrayList<>();
    for (String name :
        new String[] {"jvm_jfr", "jvm_trace", "jvm_redefine", "jvm_notifications", "jvm_ognl"}) {
      Map<String, Object> probe = new LinkedHashMap<>();
      probe.put("name", name);
      probe.put("title", name.equals("jvm_jfr") ? "JVM JFR" : name);
      probe.put("checkpoints", true);
      probe.put("mutates", !name.equals("jvm_notifications"));
      probe.put("reverts", true);
      probe.put("scope", Scope.forCapture(name).wire());
      // The generation of what jvm_trace accepts, so a client asking for an option a later
      // generation added is refused before it arms rather than handed a capture that ignored it.
      // The bundled generation, as describe() reports it: hello must not need the diag agent
      // attached, and an older agent already on the system class path is still refused at arm
      // time by ClassOperations' requireProbeApiFor.
      if (name.equals("jvm_trace"))
        probe.put("version", com.flanksource.telltale.diag.agent.DiagAgent.PROBE_API_VERSION);
      probes.add(probe);
    }
    return probes;
  }

  @SuppressWarnings("unchecked")
  public Object invoke(String action, Object[] values, String[] signature)
      throws MBeanException, ReflectionException {
    try {
      return dispatch(action, (Map<String, Object>) values[0]);
    } catch (Exception error) {
      throw new MBeanException(error, error.toString());
    }
  }

  @SuppressWarnings("unchecked")
  private Object dispatch(String action, Map<String, Object> request) throws Exception {
    switch (action) {
      case "configure":
        if (!configuration.equals(request))
          throw new IllegalStateException(
              "diagnostic runtime already initialized with different mutation or extension"
                  + " configuration");
        return null;
      case "describe":
        return describe();
      case "catalog":
        return diagnostics.catalog();
      case "http":
        return http.handle(request);
      case "registerAdapter":
        registerAdapter(request);
        return null;
      case "closeOwner":
        closeOwner(Diagnostics.required(request, "owner"));
        return null;
      case "call":
        return diagnostics.call(
            Diagnostics.required(request, "operation"),
            (Map<String, Object>) request.get("params"),
            Boolean.TRUE.equals(request.get("allowMutation")));
      case "captureOpen":
        return openCapture(request);
      default:
        return captureAction(action, Diagnostics.required(request, "id"));
    }
  }

  private static Map<String, Object> describe() {
    Map<String, Object> result = new LinkedHashMap<>();
    result.put("apiVersion", API_VERSION);
    result.put("probeApiVersion", com.flanksource.telltale.diag.agent.DiagAgent.PROBE_API_VERSION);
    return result;
  }

  private void registerAdapter(Map<String, Object> request) {
    String owner = Diagnostics.required(request, "owner");
    Object jwks = request.get("jwks");
    if (jwks != null && !(jwks instanceof String)) jwks = Agent.JSON.toJson(jwks);
    JwtVerifier verifier =
        new JwtVerifier(
            Diagnostics.text(request, "issuer", null),
            Diagnostics.text(request, "audience", null),
            (String) jwks,
            Diagnostics.text(request, "jwksUrl", null));
    synchronized (captures) {
      if (verifiers.containsKey(owner))
        throw new IllegalStateException("adapter already registered: " + owner);
      verifiers.put(owner, verifier);
    }
  }

  JwtVerifier verifier(String owner) {
    synchronized (captures) {
      return owner == null ? null : verifiers.get(owner);
    }
  }

  private void closeOwner(String owner) throws Exception {
    List<String> ids = new ArrayList<>();
    synchronized (captures) {
      verifiers.remove(owner);
      for (Map.Entry<String, String> entry : owners.entrySet())
        if (entry.getValue().equals(owner)) ids.add(entry.getKey());
    }
    Exception failure = null;
    for (String id : ids)
      try {
        captureAction("captureClose", id);
      } catch (Exception error) {
        if (failure == null) failure = error;
        else failure.addSuppressed(error);
      }
    if (failure != null) throw failure;
  }

  @SuppressWarnings("unchecked")
  Map<String, Object> openCapture(Map<String, Object> request) throws Exception {
    String key = Diagnostics.required(request, "id"), kind = Diagnostics.required(request, "kind");
    Scope scope = Scope.forCapture(kind);
    boolean allow = Boolean.TRUE.equals(request.get("allowMutation"));
    synchronized (captures) {
      if (captures.containsKey(key)) throw new IllegalArgumentException("duplicate capture " + key);
      Map<String, Object> params = (Map<String, Object>) request.get("params");
      Capture capture =
          kind.equals("jvm_jfr")
              ? new Recording(Agent.JSON.toJsonTree(params).getAsJsonObject())
              : diagnostics.capture(kind, params, key, allow);
      captures.put(key, capture);
      captureScopes.put(key, scope);
      if (request.containsKey("owner")) owners.put(key, (String) request.get("owner"));
      Map<String, Object> result = new LinkedHashMap<>();
      result.put("id", key);
      result.put("streaming", capture.streaming());
      result.put("contentType", capture.contentType());
      result.put("fileName", capture.fileName());
      return result;
    }
  }

  /** The capture an adapter may act on, or a {@link RuntimeHttp.Failure} (404/409). */
  Capture owned(String id, String owner) throws RuntimeHttp.Failure {
    synchronized (captures) {
      Capture capture = captures.get(id);
      if (capture == null) throw new RuntimeHttp.Failure(404, "unknown capture " + id);
      if (!Objects.equals(owners.get(id), owner))
        throw new RuntimeHttp.Failure(409, "capture does not belong to this adapter");
      return capture;
    }
  }

  Scope captureScope(String id) {
    synchronized (captures) {
      return captureScopes.get(id);
    }
  }

  Object captureAction(String action, String key) throws Exception {
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
        case "captureReport":
          return capture.report();
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
            captureScopes.remove(key);
          }
          return null;
        default:
          throw new IllegalArgumentException("unknown runtime action " + action);
      }
    }
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
    for (String action : ACTIONS)
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
