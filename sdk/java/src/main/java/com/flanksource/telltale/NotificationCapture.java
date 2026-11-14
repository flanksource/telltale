package com.flanksource.telltale;

import com.flanksource.telltale.diag.jmx.NotificationHub;
import java.nio.charset.StandardCharsets;
import java.util.*;

final class NotificationCapture implements Capture {
  private final NotificationHub hub;
  private final String id;
  private long cursor;
  private boolean closed;

  @SuppressWarnings("unchecked")
  NotificationCapture(NotificationHub hub, Map<String, Object> params) throws Exception {
    this.hub = hub;
    Object filters = params.getOrDefault("types", Collections.emptyList());
    if (!(filters instanceof List))
      throw new IllegalArgumentException("types must be an array of strings");
    for (Object filter : (List<?>) filters)
      if (!(filter instanceof String))
        throw new IllegalArgumentException("types must be an array of strings");
    double capacity = ((Number) params.getOrDefault("capacity", 500)).doubleValue();
    if (capacity != Math.rint(capacity))
      throw new IllegalArgumentException("capacity must be an integer");
    id =
        hub.register(
            Diagnostics.required(params, "mbean"),
            (List<String>) filters,
            NotificationHub.checkCapacity((long) capacity));
  }

  @SuppressWarnings("unchecked")
  public synchronized byte[] dump() {
    if (closed) throw new IllegalStateException("notification capture is closed");
    Map<String, Object> batch = hub.read(id, cursor, NotificationHub.MAX_CAPACITY);
    List<Map<String, Object>> events =
        new ArrayList<>((List<Map<String, Object>>) batch.get("events"));
    long lost = ((Number) batch.get("dropped")).longValue();
    if (lost > 0) {
      Map<String, Object> loss = new LinkedHashMap<>();
      loss.put("timeStamp", System.currentTimeMillis());
      loss.put("type", "telltale.dropped");
      loss.put("dropped", lost);
      events.add(0, loss);
    }
    cursor = ((Number) batch.get("nextSeq")).longValue();
    return Agent.JSON.toJson(events).getBytes(StandardCharsets.UTF_8);
  }

  public void freeze() {
    hub.freeze(id);
  }

  public synchronized void close() {
    if (!closed) {
      hub.deregister(id);
      closed = true;
    }
  }

  public boolean streaming() {
    return true;
  }

  public String contentType() {
    return "application/json";
  }

  public String fileName() {
    return "notifications.json";
  }
}
