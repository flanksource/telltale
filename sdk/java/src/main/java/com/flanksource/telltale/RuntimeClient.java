package com.flanksource.telltale;

import java.lang.management.ManagementFactory;
import java.util.*;
import javax.management.*;

final class RuntimeClient {
  RuntimeClient(Map<String, Object> config) throws Exception {
    RuntimeService.acquire(config);
  }

  static Object request(String action, Map<String, Object> request) throws Exception {
    try {
      return ManagementFactory.getPlatformMBeanServer()
          .invoke(
              new ObjectName(RuntimeService.NAME),
              action,
              new Object[] {request},
              new String[] {Map.class.getName()});
    } catch (MBeanException failure) {
      throw failure.getTargetException();
    }
  }

  /** The registered runtime's API version; a runtime without {@code describe} predates it (0). */
  static int apiVersion() throws Exception {
    for (MBeanOperationInfo operation :
        ManagementFactory.getPlatformMBeanServer()
            .getMBeanInfo(new ObjectName(RuntimeService.NAME))
            .getOperations())
      if (operation.getName().equals("describe")) {
        Object value = ((Map<?, ?>) request("describe", Collections.emptyMap())).get("apiVersion");
        if (!(value instanceof Number))
          throw new IllegalStateException("diagnostic runtime describe returned no apiVersion");
        return ((Number) value).intValue();
      }
    return 0;
  }

  @SuppressWarnings("unchecked")
  List<Map<String, Object>> catalog() throws Exception {
    return (List<Map<String, Object>>) request("catalog", Collections.emptyMap());
  }

  Object call(String operation, Map<String, Object> params, boolean allow) throws Exception {
    Map<String, Object> request = new LinkedHashMap<>();
    request.put("operation", operation);
    request.put("params", params);
    request.put("allowMutation", allow);
    return request("call", request);
  }

  @SuppressWarnings("unchecked")
  Capture capture(String kind, Map<String, Object> params, String id, boolean allow)
      throws Exception {
    Map<String, Object> request = new LinkedHashMap<>();
    request.put("kind", kind);
    request.put("params", params);
    request.put("id", id);
    request.put("allowMutation", allow);
    Map<String, Object> description = (Map<String, Object>) request("captureOpen", request);
    return new Capture() {
      private boolean closed;

      public byte[] dump() throws Exception {
        return (byte[]) request("captureDump", Collections.singletonMap("id", id));
      }

      public byte[] finish() throws Exception {
        return (byte[]) request("captureFinish", Collections.singletonMap("id", id));
      }

      public void renew() throws Exception {
        request("captureRenew", Collections.singletonMap("id", id));
      }

      public void close() throws Exception {
        if (!closed) {
          request("captureClose", Collections.singletonMap("id", id));
          closed = true;
        }
      }

      public boolean streaming() {
        return Boolean.TRUE.equals(description.get("streaming"));
      }

      public boolean complete() {
        try {
          return Boolean.TRUE.equals(
              request("captureComplete", Collections.singletonMap("id", id)));
        } catch (Exception error) {
          throw new IllegalStateException("read capture completion", error);
        }
      }

      @SuppressWarnings("unchecked")
      public Map<String, Object> report() {
        try {
          return (Map<String, Object>) request("captureReport", Collections.singletonMap("id", id));
        } catch (Exception error) {
          throw new IllegalStateException("read capture report", error);
        }
      }

      public String contentType() {
        return (String) description.get("contentType");
      }

      public String fileName() {
        return (String) description.get("fileName");
      }
    };
  }
}
