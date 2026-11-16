package com.flanksource.telltale.observer;

import java.lang.ref.*;
import java.lang.reflect.Modifier;
import java.time.Instant;
import java.util.*;
import org.objectweb.asm.*;
import org.objectweb.asm.commons.AdviceAdapter;
import org.objectweb.asm.commons.Method;

/** Observes the existing Instrumenter implementation without changing a returned Context. */
final class OtelOperations {
  private static final int MAX_INFLIGHT = 32768;
  private final ReferenceQueue<Object> collected = new ReferenceQueue<>();
  private final Map<Identity, Map<String, Object>> started = new HashMap<>();
  private final ThreadLocal<Map<String, Object>> current = new ThreadLocal<>();
  private final java.util.concurrent.atomic.AtomicLong unmatched = new java.util.concurrent.atomic.AtomicLong();
  private static final Method CALLBACK = new Method("operation",
      "(Ljava/lang/String;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Object;)V");

  static boolean isInstrumenter(String name) {
    return name.equals(OtelTargets.name("instrumenter"))
        || name.equals(OtelTargets.name("agent.instrumenter"));
  }

  static MethodVisitor weave(MethodVisitor visitor, int access, String name, String descriptor,
      String owner, boolean[] changed) {
    boolean start = name.equals("doStartImpl");
    boolean end = name.equals("doEnd");
    if (!start && !end) return null;
    String prefix = owner.contains("javaagent/shaded/") ? "agent." : "";
    if (!descriptor.equals(OtelTargets.name(prefix + (start ? "start.descriptor" : "end.descriptor"))))
      throw new IllegalStateException("unsupported OTel Instrumenter method " + name + descriptor);
    changed[0] = true;
    return new OperationAdvice(visitor, access, name, descriptor, owner, start);
  }

  synchronized void clear() { started.clear(); current.remove(); }
  long unmatchedEnds() { return unmatched.get(); }
  Object active(String signal) {
    Map<String, Object> operation = current.get();
    return operation != null && signal.equals(operation.get("signal")) ? operation.get("operationId") : null;
  }

  void observe(Object[] values) {
    String phase = (String) values[0];
    Object context = values[2];
    Map<String, Object> metadata;
    synchronized (this) {
      for (Reference<?> dead; (dead = collected.poll()) != null;) started.remove(dead);
      if (phase.equals("start")) {
        if (started.size() >= MAX_INFLIGHT) throw new IllegalStateException("OTel observer in-flight operation limit exceeded");
        metadata = new LinkedHashMap<>();
        metadata.put("operationId", UUID.randomUUID().toString());
        metadata.put("instrumentationName", values[1]);
        metadata.put("name", values[7]);
        metadata.put("kind", values[8] == null ? "" : values[8].toString().toLowerCase(Locale.ROOT));
        metadata.put("startNanos", System.nanoTime());
        metadata.put("startedAt", values[9] == null ? Instant.now().toString() : values[9].toString());
        metadata.put("startAttributes", attributes(values[6]));
        metadata.put("signal", signal((Map<?, ?>) metadata.get("startAttributes")));
        addSpanContext(metadata, context);
        started.put(new Identity(context, collected), metadata);
      } else {
        metadata = started.remove(new Identity(context, null));
        if (metadata == null) { unmatched.incrementAndGet(); return; }
        metadata.put("durationNs", System.nanoTime() - ((Number) metadata.get("startNanos")).longValue());
        metadata.put("endAttributes", attributes(values[6]));
        if (values[5] instanceof Throwable) metadata.put("error", values[5].toString());
      }
    }
    Map<String, Object> merged = new LinkedHashMap<>((Map<String, Object>) metadata.get("startAttributes"));
    if (metadata.get("endAttributes") instanceof Map) merged.putAll((Map<String, Object>) metadata.get("endAttributes"));
    metadata.put("attributes", merged);
    metadata.put("phase", phase);
    current.set(metadata);
    try {
      Map<String, Object> event = new LinkedHashMap<>(metadata);
      event.put("request", values[3]); event.put("context", values[2]);
      ObserverRuntime.operation(Collections.unmodifiableMap(event));
    }
    finally { current.remove(); }
  }

  private static String signal(Map<?, ?> attrs) {
    for (Object key : attrs.keySet()) {
      String name = key.toString();
      if (name.startsWith("http.")) return "http";
      if (name.startsWith("db.")) return "sql";
      if (name.startsWith("messaging.")) return "jms";
    }
    return "";
  }

  static Map<String, Object> attributes(Object attributes) {
    if (attributes == null) return Collections.emptyMap();
    Object raw = attributes instanceof Map ? attributes : invoke(attributes, "asMap");
    if (!(raw instanceof Map)) throw new IllegalStateException("OTel Attributes.asMap did not return a map");
    Map<String, Object> result = new LinkedHashMap<>();
    for (Map.Entry<?, ?> attr : ((Map<?, ?>) raw).entrySet())
      result.put(attr.getKey() instanceof String ? (String) attr.getKey() : String.valueOf(invoke(attr.getKey(), "getKey")), attr.getValue());
    return result;
  }

  static Object invoke(Object receiver, String method) {
    if (receiver == null) throw new IllegalArgumentException("missing OTel receiver for " + method);
    try { return publicMethod(receiver.getClass(), method).invoke(receiver); }
    catch (ReflectiveOperationException error) { throw new IllegalStateException("read OTel " + method, error); }
  }

  private static java.lang.reflect.Method publicMethod(Class<?> type, String name) throws NoSuchMethodException {
    if (Modifier.isPublic(type.getModifiers())) {
      try { return type.getMethod(name); } catch (NoSuchMethodException absent) { /* Search public contracts. */ }
    }
    for (Class<?> contract : type.getInterfaces()) {
      try { return publicMethod(contract, name); } catch (NoSuchMethodException absent) { /* Try the next contract. */ }
    }
    if (type.getSuperclass() != null) return publicMethod(type.getSuperclass(), name);
    throw new NoSuchMethodException(type.getName() + "." + name);
  }

  private static void addSpanContext(Map<String, Object> metadata, Object context) {
    try {
      boolean agent = context.getClass().getName().contains("javaagent.shaded");
      String name = OtelTargets.name(agent ? "agent.span" : "span");
      ClassLoader loader = context.getClass().getClassLoader();
      Class<?> span = Class.forName(name, false, loader);
      Class<?> contextType = Class.forName(OtelTargets.name(agent ? "agent.context" : "context"), false, loader);
      Object spanContext = invoke(span.getMethod("fromContext", contextType).invoke(null, context), "getSpanContext");
      metadata.put("traceId", invoke(spanContext, "getTraceId"));
      metadata.put("spanId", invoke(spanContext, "getSpanId"));
      metadata.put("sampled", invoke(spanContext, "isSampled"));
    } catch (ReflectiveOperationException error) { throw new IllegalStateException("read OTel operation correlation", error); }
  }

  private static final class Identity extends WeakReference<Object> {
    final int hash;
    Identity(Object object, ReferenceQueue<Object> queue) { super(object, queue); hash = System.identityHashCode(object); }
    public int hashCode() { return hash; }
    public boolean equals(Object other) {
      return other == this || other instanceof Identity && get() != null && get() == ((Identity) other).get();
    }
  }

  private static final class OperationAdvice extends AdviceAdapter {
    final String owner;
    final boolean start;
    int attributes, spanName, spanKind, context;
    int assignments;
    int names, kinds;
    OperationAdvice(MethodVisitor visitor, int access, String name, String descriptor, String owner, boolean start) {
      super(Opcodes.ASM9, visitor, access, name, descriptor); this.owner = owner; this.start = start;
    }
    protected void onMethodEnter() {
      attributes = newLocal(Type.getType(Object.class)); spanName = newLocal(Type.getType(Object.class)); spanKind = newLocal(Type.getType(Object.class));
      context = newLocal(Type.getType(Object.class));
      visitInsn(ACONST_NULL); storeLocal(attributes); visitInsn(ACONST_NULL); storeLocal(spanName); visitInsn(ACONST_NULL); storeLocal(spanKind);
      visitInsn(ACONST_NULL); storeLocal(context);
    }
    public void visitMethodInsn(int opcode, String owner, String name, String descriptor, boolean contract) {
      if (name.equals("setAllAttributes") && (owner.endsWith("/SpanBuilder") || owner.endsWith("/Span"))) {
        dup(); storeLocal(attributes); assignments++;
      }
      super.visitMethodInsn(opcode, owner, name, descriptor, contract);
      if (start && name.equals("extract") && owner.endsWith("/SpanNameExtractor")) { dup(); storeLocal(spanName); names++; }
      if (start && name.equals("extract") && owner.endsWith("/SpanKindExtractor")) { dup(); storeLocal(spanKind); kinds++; }
    }
    protected void onMethodExit(int opcode) {
      if (opcode == ATHROW) return;
      if (start) storeLocal(context);
      push(start ? "start" : "end");
      loadThis(); getField(Type.getObjectType(owner), "instrumentationName", Type.getType(String.class));
      if (start) loadLocal(context); else loadArg(0);
      loadArg(1);
      if (start) { visitInsn(ACONST_NULL); visitInsn(ACONST_NULL); }
      else { loadArg(2); loadArg(3); }
      loadLocal(attributes); loadLocal(spanName); loadLocal(spanKind); loadArg(start ? 2 : 4);
      invokeStatic(ObserverTransformer.BRIDGE, CALLBACK);
      if (start) { loadLocal(context); checkCast(Type.getReturnType(methodDesc)); }
    }
    public void visitEnd() {
      if (assignments != 1) throw new IllegalStateException("unsupported OTel Instrumenter attribute anchors: " + assignments);
      if (start && (names != 1 || kinds != 1))
        throw new IllegalStateException("unsupported OTel Instrumenter name/kind anchors: " + names + "/" + kinds);
      super.visitEnd();
    }
  }
}
