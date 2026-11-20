package com.flanksource.telltale.observer;

import com.google.protobuf.ByteString;
import io.opentelemetry.proto.collector.trace.v1.ExportTraceServiceRequest;
import io.opentelemetry.proto.common.v1.AnyValue;
import io.opentelemetry.proto.common.v1.ArrayValue;
import io.opentelemetry.proto.common.v1.InstrumentationScope;
import io.opentelemetry.proto.common.v1.KeyValue;
import io.opentelemetry.proto.resource.v1.Resource;
import io.opentelemetry.proto.trace.v1.*;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.*;

/** Mirrors the original export call; no provider, processor, or exporter is replaced. */
final class OtelSpans implements ObserverAdapter {
  private final Map<String, String> observed = new LinkedHashMap<String, String>(128, .75f, true) {
    protected boolean removeEldestEntry(Map.Entry<String, String> entry) { return size() > 32768; }
  };
  public String id() { return "otel-spans"; }
  public Set<String> kinds() { return Collections.singleton("spans"); }
  public boolean matchesType(String name, Set<String> hierarchy) {
    return hierarchy.contains(OtelTargets.name("exporter"))
        || hierarchy.contains(OtelTargets.name("agent.exporter"));
  }
  public boolean matchesMethod(String name, String descriptor) {
    return name.equals("export") && (descriptor.equals("(Ljava/util/Collection;)L"
        + OtelTargets.name("result").replace('.', '/') + ";")
        || descriptor.equals("(Ljava/util/Collection;)L"
        + OtelTargets.name("agent.result").replace('.', '/') + ";"));
  }
  public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) {
    if (!(args[0] instanceof Collection)) throw new IllegalArgumentException("OTel exporter argument is not a Collection");
    for (Object span : (Collection<?>) args[0]) capture(span);
    return null;
  }
  public Object onExit(Object token, Object result, Throwable error) { return result; }
  public synchronized void onDisarm(String kind) { observed.clear(); }

  private void capture(Object data) {
    io.opentelemetry.proto.trace.v1.Span span = nativeSpan(data);
    Object resource = call(data, "getResource");
    Object scope = call(data, "getInstrumentationScopeInfo");
    Map<String, Object> resourceAttrs = OtelOperations.attributes(call(resource, "getAttributes"));
    ResourceSpans.Builder resourceSpan = ResourceSpans.newBuilder()
        .setResource(Resource.newBuilder().addAllAttributes(attrs(resourceAttrs)))
        .addScopeSpans(ScopeSpans.newBuilder().setScope(InstrumentationScope.newBuilder()
            .setName(string(scope, "getName")).setVersion(nullable(call(scope, "getVersion")))
            .addAllAttributes(attrs(OtelOperations.attributes(call(scope, "getAttributes")))))
            .setSchemaUrl(nullable(call(scope, "getSchemaUrl"))).addSpans(span));
    Object schema = call(resource, "getSchemaUrl");
    if (schema != null) resourceSpan.setSchemaUrl(schema.toString());
    byte[] nativeBytes = ExportTraceServiceRequest.newBuilder().addResourceSpans(resourceSpan).build().toByteArray();
    String trace = string(data, "getTraceId"), id = string(data, "getSpanId");
    String key = trace + "/" + id;
    String digest;
    try { digest = Base64.getEncoder().encodeToString(MessageDigest.getInstance("SHA-256").digest(nativeBytes)); }
    catch (java.security.NoSuchAlgorithmException error) { throw new IllegalStateException(error); }
    synchronized (this) {
      String before = observed.put(key, digest);
      if (digest.equals(before)) return;
      if (before != null) throw new IllegalStateException("conflicting OTel exported span " + key);
    }
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("traceId", trace); row.put("spanId", id); row.put("parentId", string(data, "getParentSpanId"));
    row.put("operation", string(data, "getName")); row.put("kind", call(data, "getKind").toString().toLowerCase(Locale.ROOT));
    long start = number(data, "getStartEpochNanos"), end = number(data, "getEndEpochNanos");
    row.put("startedAt", Instant.ofEpochSecond(start / 1000000000L, start % 1000000000L).toString());
    row.put("durationMs", (end - start) / 1000000d);
    row.put("status", call(call(data, "getStatus"), "getStatusCode").toString().toLowerCase(Locale.ROOT));
    row.put("attributes", OtelOperations.attributes(call(data, "getAttributes")));
    row.put("resource", resourceAttrs); row.put("source", Collections.singletonMap("type", "otlp"));
    row.put("native", Base64.getEncoder().encodeToString(nativeBytes));
    ObserverRuntime.emit("spans", row);
  }

  private static io.opentelemetry.proto.trace.v1.Span nativeSpan(Object data) {
    io.opentelemetry.proto.trace.v1.Span.Builder span = io.opentelemetry.proto.trace.v1.Span.newBuilder()
        .setTraceId(hex(string(data, "getTraceId"))).setSpanId(hex(string(data, "getSpanId")))
        .setName(string(data, "getName"))
        .setStartTimeUnixNano(number(data, "getStartEpochNanos")).setEndTimeUnixNano(number(data, "getEndEpochNanos"))
        .setKind(io.opentelemetry.proto.trace.v1.Span.SpanKind.valueOf("SPAN_KIND_" + call(data, "getKind")))
        .addAllAttributes(attrs(OtelOperations.attributes(call(data, "getAttributes"))));
    int count = ((Collection<?>) span.getAttributesList()).size();
    span.setDroppedAttributesCount((int) number(data, "getTotalAttributeCount") - count);
    Object status = call(data, "getStatus");
    span.setStatus(Status.newBuilder().setCode(Status.StatusCode.valueOf("STATUS_CODE_" + call(status, "getStatusCode")))
        .setMessage(string(status, "getDescription")));
    Object context = call(data, "getSpanContext");
    Object parent = call(data, "getParentSpanContext");
    if (Boolean.TRUE.equals(call(parent, "isValid"))) span.setParentSpanId(hex(string(parent, "getSpanId")));
    span.setFlags(flags(context, parent));
    span.setTraceState(traceState(context));
    for (Object event : (List<?>) call(data, "getEvents")) {
      List<KeyValue> attributes = attrs(OtelOperations.attributes(call(event, "getAttributes")));
      span.addEvents(io.opentelemetry.proto.trace.v1.Span.Event.newBuilder()
          .setName(string(event, "getName")).setTimeUnixNano(number(event, "getEpochNanos"))
          .addAllAttributes(attributes).setDroppedAttributesCount((int) number(event, "getTotalAttributeCount") - attributes.size()));
    }
    span.setDroppedEventsCount((int) number(data, "getTotalRecordedEvents") - span.getEventsCount());
    for (Object link : (List<?>) call(data, "getLinks")) {
      Object linked = call(link, "getSpanContext");
      List<KeyValue> attributes = attrs(OtelOperations.attributes(call(link, "getAttributes")));
      span.addLinks(io.opentelemetry.proto.trace.v1.Span.Link.newBuilder()
          .setTraceId(hex(string(linked, "getTraceId"))).setSpanId(hex(string(linked, "getSpanId")))
          .setFlags(flags(linked, linked)).setTraceState(traceState(linked))
          .addAllAttributes(attributes).setDroppedAttributesCount((int) number(link, "getTotalAttributeCount") - attributes.size()));
    }
    span.setDroppedLinksCount((int) number(data, "getTotalRecordedLinks") - span.getLinksCount());
    return span.build();
  }

  private static List<KeyValue> attrs(Map<String, Object> values) {
    List<KeyValue> result = new ArrayList<>();
    for (Map.Entry<String, Object> entry : values.entrySet())
      result.add(KeyValue.newBuilder().setKey(entry.getKey()).setValue(value(entry.getValue())).build());
    return result;
  }

  private static AnyValue value(Object value) {
    AnyValue.Builder builder = AnyValue.newBuilder();
    if (value instanceof String) return builder.setStringValue((String) value).build();
    if (value instanceof Boolean) return builder.setBoolValue((Boolean) value).build();
    if (value instanceof Float || value instanceof Double) return builder.setDoubleValue(((Number) value).doubleValue()).build();
    if (value instanceof Number) return builder.setIntValue(((Number) value).longValue()).build();
    if (value instanceof List) {
      ArrayValue.Builder array = ArrayValue.newBuilder();
      for (Object item : (List<?>) value) array.addValues(value(item));
      return builder.setArrayValue(array).build();
    }
    throw new IllegalArgumentException("unsupported OTel attribute value " + (value == null ? "null" : value.getClass().getName()));
  }

  private static Object call(Object receiver, String method) { return OtelOperations.invoke(receiver, method); }
  private static int flags(Object context, Object remote) {
    return (((Number) call(call(context, "getTraceFlags"), "asByte")).byteValue() & 255)
        | 256 | (Boolean.TRUE.equals(call(remote, "isRemote")) ? 512 : 0);
  }
  private static String traceState(Object context) {
    Map<?, ?> values = (Map<?, ?>) call(call(context, "getTraceState"), "asMap");
    List<String> entries = new ArrayList<>();
    for (Map.Entry<?, ?> entry : values.entrySet()) entries.add(entry.getKey() + "=" + entry.getValue());
    return String.join(",", entries);
  }
  private static String string(Object receiver, String method) { return nullable(call(receiver, method)); }
  private static String nullable(Object value) { return value == null ? "" : value.toString(); }
  private static long number(Object receiver, String method) { return ((Number) call(receiver, method)).longValue(); }
  private static ByteString hex(String hex) {
    byte[] bytes = new byte[hex.length() / 2];
    for (int i = 0; i < bytes.length; i++) {
      int high = Character.digit(hex.charAt(i * 2), 16), low = Character.digit(hex.charAt(i * 2 + 1), 16);
      if (high < 0 || low < 0) throw new IllegalArgumentException("invalid OTel ID " + hex);
      bytes[i] = (byte) ((high << 4) | low);
    }
    return ByteString.copyFrom(bytes);
  }
}
