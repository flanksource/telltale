package com.flanksource.telltale;

import com.flanksource.telltale.observer.ObserverRuntime;
import java.lang.management.ManagementFactory;
import java.util.*;

final class AgentResource {
  private AgentResource() {}

  @SuppressWarnings("unchecked")
  static Map<String, Object> from(Map<String, Object> config) {
    Object supplied = config.get("resource");
    if (supplied != null && !(supplied instanceof Map))
      throw new IllegalArgumentException("agent resource must be an object");
    Map<String, Object> resource = supplied == null ? new LinkedHashMap<>()
        : new LinkedHashMap<>((Map<String, Object>) supplied);
    Object service = config.containsKey("service") ? config.get("service") : "java";
    if (!(service instanceof String) || ((String) service).isEmpty())
      throw new IllegalArgumentException("agent service must be a nonempty string");
    resource.put("service.name", service);
    resource.put("process.pid", ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
    resource.put("telltale.source.instance", ObserverRuntime.SOURCE);
    return resource;
  }
}
