package com.flanksource.telltale;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverOptions;
import com.flanksource.telltale.observer.profiles.ProfileEncoder;
import java.nio.charset.StandardCharsets;
import java.util.*;

final class ObserverCapture implements Capture {
  private final Object subscription;
  private final String kind, id, source;
  private final boolean top;
  private final int limit;
  private final long interval;
  private final Map<String, Object> resource = new LinkedHashMap<>();
  private long sequence;
  private boolean frozen;

  ObserverCapture(String kind, Map<String, Object> params, String id, Map<String, Object> configuredResource) throws Exception {
    this.kind = kind; this.id = id;
    top = kind.endsWith("_top");
    limit = ObserverOptions.positiveInt(params, "limit", 50, 10000);
    interval = ObserverOptions.positiveInt(params, "pollIntervalMs", 5000, 3600000);
    resource.putAll(configuredResource);
    java.lang.instrument.Instrumentation instrumentation = com.flanksource.telltale.diag.hotpatch.InstrumentationService.instrumentation();
    Class<?> owner = Class.forName("com.flanksource.telltale.observer.ObserverRuntime", true, RuntimeOwner.loader());
    owner.getMethod("install", java.lang.instrument.Instrumentation.class).invoke(null, instrumentation);
    source = (String) owner.getField("SOURCE").get(null);
    resource.put("telltale.source.instance", source);
    subscription = owner.getMethod("subscribe", String.class, String.class, Map.class).invoke(null, kind, id, params);
  }

  public synchronized byte[] dump() throws Exception {
    if (!top) return ObserverRuntime.json(call("drain")).getBytes(StandardCharsets.UTF_8);
    long begin = ObserverRuntime.epochNanos();
    List<Map<String, Object>> rows = new ArrayList<>((List<Map<String, Object>>) call("snapshot"));
    int observed = rows.size();
    rows.sort((first, second) -> Long.compare(((Number) second.get("elapsedNs")).longValue(), ((Number) first.get("elapsedNs")).longValue()));
    if (rows.size() > limit) rows = new ArrayList<>(rows.subList(0, limit));
    Map<String, Object> snapshot = new LinkedHashMap<>();
    snapshot.put("kind", kind); snapshot.put("captureId", id);
    snapshot.put("source", source); snapshot.put("profileId", UUID.randomUUID().toString());
    snapshot.put("sequence", ++sequence); snapshot.put("startedAtNanos", begin);
    snapshot.put("completedAtNanos", ObserverRuntime.epochNanos());
    snapshot.put("observed", observed); snapshot.put("limit", limit); snapshot.put("dropped", dropped());
    snapshot.put("complete", true); snapshot.put("pollIntervalMs", interval);
    snapshot.put("resource", resource); snapshot.put("rows", rows);
    return ProfileEncoder.encode(snapshot);
  }

  public String contentType() { return top ? ProfileEncoder.CONTENT_TYPE : "application/json"; }
  public String fileName() { return top ? "snapshot.pb" : "records.json"; }
  public boolean streaming() { return true; }
  public long intervalMillis() { return top ? interval : 250; }
  public long dropped() { return ((Number) call("dropped")).longValue(); }
  public void renew() { call("renew"); }
  public boolean complete() { return Boolean.TRUE.equals(call("expired")); }
  public Map<String, Object> report() { return (Map<String, Object>) call("report"); }
  public synchronized void freeze() throws Exception {
    if (frozen) return;
    call("close");
    frozen = true;
  }
  public synchronized byte[] finish() throws Exception {
    if (top) { byte[] last = dump(); freeze(); return last; }
    freeze(); return dump();
  }
  public void close() throws Exception { freeze(); }

  private Object call(String method) {
    try { return subscription.getClass().getMethod(method).invoke(subscription); }
    catch (java.lang.reflect.InvocationTargetException error) {
      Throwable cause = error.getCause();
      if (cause instanceof RuntimeException) throw (RuntimeException) cause;
      throw new IllegalStateException("observer capture " + method, cause);
    } catch (ReflectiveOperationException error) { throw new IllegalStateException("observer capture " + method, error); }
  }
}
