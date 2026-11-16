package com.flanksource.telltale;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.flanksource.telltale.auth.AuthException;
import com.flanksource.telltale.auth.JwtVerifier;
import com.flanksource.telltale.auth.Principal;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.*;

/**
 * Authenticates and routes adapter HTTP requests (WAR servlet and agent listener). Every reply is
 * a map of status, contentType, body bytes and headers; failures never escape as exceptions.
 */
final class RuntimeHttp {
  static final String JSON_TYPE = "application/json; charset=UTF-8";
  static final String COMPLETE_HEADER = "X-Telltale-Complete";

  /** What the capture withheld (e.g. jvm_trace {filtered, omitted}), as JSON, when it says. */
  static final String REPORT_HEADER = "X-Telltale-Report";
  private static final int MAX_ARTIFACT = 64 * 1024 * 1024;
  private static final String CAPTURES = "/captures/";

  /** A request failure with the HTTP status it maps to. */
  static final class Failure extends Exception {
    final int status;

    Failure(int status, String message) {
      super(message);
      this.status = status;
    }
  }

  private final RuntimeService runtime;

  RuntimeHttp(RuntimeService runtime) {
    this.runtime = runtime;
  }

  Map<String, Object> handle(Map<String, Object> envelope) {
    try {
      return route(envelope);
    } catch (AuthException error) {
      return error(error.status(), error.getMessage());
    } catch (Failure error) {
      return error(error.status, error.getMessage());
    } catch (IllegalArgumentException | JsonProcessingException error) {
      return error(400, error.getMessage());
    } catch (Exception error) {
      return error(500, error.toString());
    }
  }

  private Map<String, Object> route(Map<String, Object> envelope) throws Exception {
    String method = Diagnostics.required(envelope, "method");
    String path = Diagnostics.required(envelope, "path");
    String owner = Diagnostics.text(envelope, "owner", null);
    JwtVerifier verifier = runtime.verifier(owner);
    if (verifier == null) throw new Failure(401, "adapter not registered");
    Principal principal =
        verifier.verify(
            JwtVerifier.credential(
                Diagnostics.text(envelope, "telltaleAuthorization", null),
                Diagnostics.text(envelope, "authorization", null)));
    if (method.equals("GET") && path.equals("/hello")) {
      principal.require(Scope.READ);
      return json(200, hello());
    }
    if (method.equals("GET") && path.equals("/catalog")) {
      principal.require(Scope.READ);
      return json(200, runtime.diagnostics.catalog());
    }
    Map<String, Object> body = body(envelope);
    if (method.equals("POST") && path.equals("/call")) return call(principal, body);
    if (method.equals("POST") && path.equals("/captures")) return open(principal, body, owner);
    if (path.startsWith(CAPTURES)) return capture(principal, method, path, owner);
    throw new Failure(404, "unknown diagnostic route " + method + " " + path);
  }

  private Map<String, Object> call(Principal principal, Map<String, Object> body) throws Exception {
    String operation = Diagnostics.required(body, "operation");
    principal.require(runtime.diagnostics.scope(operation));
    return json(
        200,
        runtime.diagnostics.call(
            operation, params(body), Boolean.TRUE.equals(body.get("allowMutation"))));
  }

  private Map<String, Object> open(Principal principal, Map<String, Object> body, String owner)
      throws Exception {
    principal.require(Scope.forCapture(Diagnostics.required(body, "kind")));
    body.put("params", params(body));
    body.put("id", UUID.randomUUID().toString());
    body.put("owner", owner);
    return json(200, runtime.openCapture(body));
  }

  private Map<String, Object> capture(
      Principal principal, String method, String path, String owner) throws Exception {
    String id = path.substring(CAPTURES.length());
    boolean renew = id.endsWith("/renew");
    if (renew) id = id.substring(0, id.length() - "/renew".length());
    if (id.isEmpty() || id.contains("/")) throw new Failure(404, "unknown diagnostic route " + path);
    Capture capture = runtime.owned(id, owner);
    principal.require(runtime.captureScope(id));
    if (renew && method.equals("POST")) {
      runtime.captureAction("captureRenew", id);
      return reply(204, "", new byte[0]);
    }
    if (!renew && method.equals("POST")) {
      Map<String, Object> reply = artifact(capture, (byte[]) runtime.captureAction("captureDump", id));
      headers(reply).put(COMPLETE_HEADER, Boolean.toString(capture.complete()));
      putReport(reply, capture);
      return reply;
    }
    if (!renew && method.equals("DELETE")) {
      try {
        Map<String, Object> reply =
            artifact(capture, (byte[]) runtime.captureAction("captureFinish", id));
        putReport(reply, capture);
        return reply;
      } finally {
        runtime.captureAction("captureClose", id);
      }
    }
    throw new Failure(404, "unknown diagnostic route " + method + " " + path);
  }

  private Map<String, Object> hello() throws Exception {
    Map<String, Object> hello = new LinkedHashMap<>();
    hello.put("apiVersion", RuntimeService.API_VERSION);
    hello.put("protocol", 1);
    hello.put("started", runtime.started);
    Map<String, Object> platform = new LinkedHashMap<>();
    platform.put("lang", "java");
    platform.put("version", System.getProperty("java.version"));
    platform.put("vendor", System.getProperty("java.vendor"));
    hello.put("runtime", platform);
    hello.put("probes", RuntimeService.probes());
    hello.put("ops", runtime.diagnostics.catalog());
    return hello;
  }

  @SuppressWarnings("unchecked")
  private static Map<String, Object> body(Map<String, Object> envelope) throws Exception {
    String body = Diagnostics.text(envelope, "body", null);
    if (body == null || body.trim().isEmpty()) return new LinkedHashMap<>();
    Object parsed =
        com.flanksource.telltale.diag.Json.read(
            new ByteArrayInputStream(body.getBytes(StandardCharsets.UTF_8)), Object.class);
    if (!(parsed instanceof Map)) throw new IllegalArgumentException("request body must be an object");
    return (Map<String, Object>) parsed;
  }

  @SuppressWarnings("unchecked")
  private static Map<String, Object> params(Map<String, Object> body) {
    Object params = body.get("params");
    if (params == null) return new LinkedHashMap<>();
    if (!(params instanceof Map)) throw new IllegalArgumentException("params must be an object");
    return (Map<String, Object>) params;
  }

  private static Map<String, Object> artifact(Capture capture, byte[] data) {
    if (data.length > MAX_ARTIFACT) throw new IllegalStateException("artifact exceeds 64 MiB");
    Map<String, Object> reply = reply(200, capture.contentType(), data);
    headers(reply)
        .put("Content-Disposition", "attachment; filename=\"" + capture.fileName() + "\"");
    return reply;
  }

  /** Carries the capture's report beside the artifact it describes, when it has one. */
  private static void putReport(Map<String, Object> reply, Capture capture) {
    Map<String, Object> report = capture.report();
    if (report != null) headers(reply).put(REPORT_HEADER, Agent.JSON.toJson(report));
  }

  private static Map<String, Object> json(int status, Object value) {
    return reply(status, JSON_TYPE, Agent.JSON.toJson(value).getBytes(StandardCharsets.UTF_8));
  }

  static Map<String, Object> error(int status, String message) {
    return json(status, Collections.singletonMap("error", message == null ? "" : message));
  }

  private static Map<String, Object> reply(int status, String contentType, byte[] body) {
    Map<String, Object> reply = new LinkedHashMap<>();
    reply.put("status", status);
    reply.put("contentType", contentType);
    reply.put("body", body);
    reply.put("headers", new LinkedHashMap<String, String>());
    return reply;
  }

  @SuppressWarnings("unchecked")
  private static Map<String, String> headers(Map<String, Object> reply) {
    return (Map<String, String>) reply.get("headers");
  }
}
