package com.flanksource.telltale.observer.jms;

import com.flanksource.telltale.observer.ObserverAdapter;
import com.flanksource.telltale.observer.ObserverRuntime;
import java.util.*;
import java.util.function.Consumer;
import java.util.function.IntSupplier;

public final class JmsObserver implements ObserverAdapter {
  private final Consumer<Map<String, Object>> emitter;
  private final IntSupplier bodyLimit;
  private final Consumer<Throwable> failure;
  private final ThreadLocal<Deque<Call>> calls = ThreadLocal.withInitial(ArrayDeque::new);
  private final ThreadLocal<Exchange> exchange = new ThreadLocal<>();
  private final JmsPending pending = new JmsPending(8192);
  private final JmsActive active = new JmsActive(8192);
  private final Map<Object, JmsOperation> nativeOperations = new java.util.concurrent.ConcurrentHashMap<>();
  private static final Set<String> CONTRACTS = new HashSet<>(Arrays.asList(
      "MessageProducer", "MessageConsumer", "MessageListener", "CompletionListener", "JMSProducer", "JMSConsumer", "Session", "JMSContext"));

  public JmsObserver() {
    emitter = row -> ObserverRuntime.emit("jms", row);
    bodyLimit = () -> ObserverRuntime.bodyLimit("jms");
    failure = error -> ObserverRuntime.fail("jms", error);
  }

  JmsObserver(Consumer<Map<String, Object>> emitter, IntSupplier bodyLimit) {
    this.emitter = emitter;
    this.bodyLimit = bodyLimit;
    failure = error -> { throw new IllegalStateException("JMS observation failed", error); };
  }

  public String id() { return "jms"; }
  public Set<String> kinds() { return Collections.singleton("jms"); }

  public void validate(String kind, Map<String, Object> params) { JmsOptions.validate(params); }

  public Map<String, Object> project(String kind, Map<String, Object> params, Map<String, Object> row) {
    return JmsOptions.project(params, row);
  }

  public boolean matchesType(String name, Set<String> hierarchy) {
    for (String type : hierarchy) {
      if (type.startsWith("javax.jms.") || type.startsWith("jakarta.jms.")) {
        if (CONTRACTS.contains(type.substring(type.lastIndexOf('.') + 1))) return true;
      }
      if (type.equals("org.springframework.jms.core.JmsTemplate")
          || type.equals("org.springframework.jms.core.JmsMessagingTemplate")
          || type.startsWith("org.springframework.jms.core.DefaultJmsClient")
          || type.equals("org.springframework.jms.listener.AbstractMessageListenerContainer")
          || type.equals("org.springframework.jms.listener.SessionAwareMessageListener")
          || type.equals("org.springframework.jms.support.converter.MessageConverter")
          || type.equals("org.springframework.jms.listener.adapter.AbstractAdaptableMessageListener")
          || type.equals("org.springframework.messaging.core.AbstractMessageSendingTemplate")
          || type.equals("org.springframework.messaging.core.AbstractMessageReceivingTemplate")
          || type.equals("org.springframework.messaging.core.AbstractMessagingTemplate")
          || type.equals("org.springframework.messaging.handler.invocation.InvocableHandlerMethod")) return true;
    }
    return false;
  }

  public boolean matchesMethod(String name, String descriptor) {
    return name.equals("send") || name.equals("publish") || name.startsWith("receive")
        || name.equals("doSend") || name.equals("doReceive") || name.equals("doReceiveSelected")
        || name.equals("onMessage") || name.equals("onCompletion") || name.equals("onException")
        || name.startsWith("create") && name.endsWith("Message")
        || name.equals("convertAndSend") || name.equals("convertSendAndReceive")
        || name.equals("sendAndReceive") || name.equals("doSendAndReceive")
        || name.equals("doExecuteListener") || name.equals("invoke")
        || name.equals("fromMessage") || name.equals("toMessage")
        || name.equals("handleListenerException") || name.equals("commitIfNecessary")
        || name.startsWith("rollback");
  }

  public boolean matchesInvocation(String owner, String name, String descriptor) {
    String type = owner.replace('/', '.');
    if (!type.startsWith("javax.jms.") && !type.startsWith("jakarta.jms.")) return false;
    return type.endsWith(".MessageListener") && name.equals("onMessage")
        || type.endsWith(".CompletionListener") && (name.equals("onCompletion") || name.equals("onException"));
  }

  public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) {
    for (JmsOperation lost : pending.collected()) {
      active.remove(lost);
      if (!lost.ended.get()) failure.accept(new IllegalStateException("JMS Message collected before its completion callback was observed"));
    }
    if (name.equals("onCompletion") || name.equals("onException")) return completion(name, args);
    if (name.startsWith("create") && name.endsWith("Message")) return new Factory(active("send"));
    if ((name.equals("sendAndReceive") || name.equals("doSendAndReceive") || name.equals("convertSendAndReceive")) && jmsSurface(owner, receiver)) {
      Exchange existing = exchange.get();
      if (existing != null) return new ExchangeCall(existing, false);
      Exchange created = new Exchange();
      exchange.set(created);
      return new ExchangeCall(created, true);
    }
    JmsOperation current = current();
    if (name.equals("invoke") && current != null && owner.startsWith("org.springframework.messaging.")) {
      Object method = JmsAccess.read(receiver, "getMethod", current);
      if (method instanceof java.lang.reflect.Method) {
        java.lang.reflect.Method handler = (java.lang.reflect.Method) method;
        current.put("handler", handler.getDeclaringClass().getName() + "." + handler.getName());
      }
      return null;
    }
    if (isPhase(name)) return phase(name, current, args);
    String operation = operation(owner, name, receiver);
    if (operation == null) return null;
    Object message = find(args, "Message");
    JmsOperation existing = active(operation);
    if (existing != null && message != null && existing.message() != null && existing.message() != message) existing = null;
    boolean root = existing == null;
    if (existing == null) {
      Object nativeID = ObserverRuntime.activeOperation("jms");
      JmsOperation candidate = nativeID == null ? null : nativeOperations.get(nativeID);
      if (candidate != null && candidate.operation.equals(operation)) existing = nativeOperations.remove(nativeID);
    }
    JmsOperation observed = existing == null ? new JmsOperation(operation) : existing;
    if (root && !reserve(observed)) return null;
    if (message != null) bind(observed, message);
    Call call = new Call(observed, root);
    calls.get().push(call);
    try {
      configure(observed, owner, name, receiver, args);
      if (root && operation.equals("send")) configureSend(observed, receiver, args);
      if (root && operation.equals("process")) JmsAccess.message(observed, bodyLimit.getAsInt());
      return call;
    } catch (RuntimeException | Error error) {
      calls.get().remove(call);
      if (calls.get().isEmpty()) calls.remove();
      if (root) active.remove(observed);
      throw error;
    }
  }

  public Object onExit(Object token, Object result, Throwable error) {
    if (token instanceof ExchangeCall) {
      finishExchange((ExchangeCall) token, error);
      return result;
    }
    if (token instanceof Factory) {
      JmsOperation operation = ((Factory) token).operation;
      if (operation != null && JmsAccess.type(result, "Message")) bind(operation, result);
      return result;
    }
    if (token instanceof Phase) {
      Phase phase = (Phase) token;
      if (error != null) phase.operation.failed(error, phase.name);
      return result;
    }
    if (!(token instanceof Call)) return result;
    Call call = (Call) token;
    Deque<Call> stack = calls.get();
    stack.remove(call);
    if (stack.isEmpty()) calls.remove();
    JmsOperation operation = call.operation;
    if (operation.ended.get()) return result;
    if (JmsAccess.type(result, "Message")) bind(operation, result);
    if (!call.root) {
      if (error != null && operation.operation.equals("process")) operation.failed(error, operation.phase);
      return result;
    }
    Object observedResult = result instanceof Optional ? ((Optional<?>) result).orElse(null) : result;
    if (operation.operation.equals("receive") && observedResult instanceof String && bodyLimit.getAsInt() > 0) {
      JmsAccess.body((String) observedResult, bodyLimit.getAsInt(), operation);
    }
    if (error != null) {
      if (error.getClass().getName().contains("MessageConversionException")) operation.phase = "conversion";
      finish(operation, error, "error");
      return result;
    }
    if (operation.asynchronous && !operation.ended.get()) {
      if (operation.message() == null) {
        operation.metadataError("Async convenience send did not expose its Message; callback correlation unavailable");
        finish(operation, null, "incomplete");
      }
      return result;
    }
    String outcome = operation.operation.equals("receive") && observedResult == null && operation.message() == null ? "timeout" : "success";
    if (outcome.equals("success") && operation.message() == null) {
      operation.metadataError("Provider did not expose a Message; JMS envelope metadata unavailable");
      if (bodyLimit.getAsInt() > 0 && operation.operation.equals("receive") && observedResult != null && !(observedResult instanceof String))
        operation.put("bodyUnsupported", "Non-text converted payload is not inspected: " + observedResult.getClass().getName());
    }
    finish(operation, null, outcome);
    return result;
  }

  private Object completion(String name, Object[] args) {
    if (args.length == 0) return null;
    JmsOperation operation = pending.remove(args[0]);
    if (operation == null) return null;
    Throwable failure = name.equals("onException") && args.length > 1 && args[1] instanceof Throwable
        ? (Throwable) args[1] : null;
    finish(operation, failure, "success");
    return null;
  }

  private void configure(JmsOperation operation, String owner, String name, Object receiver, Object[] args) {
    if (owner.startsWith("org.springframework.") || JmsAccess.type(receiver, "org.springframework.jms.listener.SessionAwareMessageListener")) {
      operation.put("framework", "spring-jms");
    }
    Object destination = find(args, "Destination");
    if (destination == null && owner.startsWith("org.springframework.jms.") && args.length > 1 && args[0] instanceof String) {
      operation.put("destination", args[0]);
    }
    if (destination == null && JmsAccess.type(receiver, "MessageProducer")) {
      destination = JmsAccess.read(receiver, "getDestination", operation);
    }
    JmsAccess.destination(destination, "destination", operation);
    if (name.equals("doExecuteListener") && args.length > 0) {
      operation.put("transacted", JmsAccess.read(args[0], "getTransacted", operation));
      operation.put("acknowledgeMode", JmsAccess.read(args[0], "getAcknowledgeMode", operation));
      operation.put("subscription", JmsAccess.read(receiver, "getSubscriptionName", operation));
    }
  }

  private void configureSend(JmsOperation operation, Object receiver, Object[] args) {
    Object listener = find(args, "CompletionListener");
    if (listener == null && JmsAccess.type(receiver, "JMSProducer")) {
      listener = JmsAccess.read(receiver, "getAsync", operation);
    }
    operation.asynchronous = listener != null;
    Object message = operation.message();
    if (operation.asynchronous && message != null) bind(operation, message);
    if (message == null && bodyLimit.getAsInt() > 0 && JmsAccess.type(receiver, "JMSProducer")
        && args.length > 1 && args[1] instanceof String) {
      JmsAccess.body((String) args[1], bodyLimit.getAsInt(), operation);
    }
  }

  private void bind(JmsOperation operation, Object message) {
    Object existing = operation.message();
    if (existing != null && existing != message) return;
    operation.message(message);
    if (!operation.asynchronous || operation.ended.get()) return;
    if (!pending.put(message, operation)) {
      failure.accept(new IllegalStateException("JMS async pending-message limit of 8192 exceeded"));
    }
  }

  private void finish(JmsOperation operation, Throwable error, String outcome) {
    Object message = operation.message();
    if (message != null) pending.remove(message);
    Map<String, Object> row = operation.finish(error, outcome, bodyLimit.getAsInt());
    if (row != null) emit(row);
    active.remove(operation);
  }

  private boolean reserve(JmsOperation operation) {
    if (active.add(operation)) return true;
    failure.accept(new IllegalStateException("JMS active-operation limit of 8192 exceeded"));
    return false;
  }

  private void emit(Map<String, Object> row) {
    Exchange active = exchange.get();
    if (active == null) emitter.accept(row);
    else {
      if (active.rows.size() >= 4096) throw new IllegalStateException("JMS request/reply capture row limit exceeded");
      active.rows.add(row);
    }
  }

  private void finishExchange(ExchangeCall call, Throwable error) {
    if (!call.root) return;
    exchange.remove();
    if (error != null) {
      if (call.exchange.rows.isEmpty()) {
        JmsOperation failed = new JmsOperation("send");
        failed.put("startedAt", call.exchange.startedAt);
        failed.put("framework", "spring-jms");
        if (error.getClass().getName().contains("Conversion")) failed.phase = "conversion";
        Map<String, Object> row = failed.finish(error, "error", 0);
        row.put("durationMs", (System.nanoTime() - call.exchange.startedNanos) / 1000000.0);
        call.exchange.rows.add(row);
      }
      Map<String, Object> last = call.exchange.rows.get(call.exchange.rows.size() - 1);
      if (!"error".equals(last.get("outcome"))) {
        last.put("outcome", "error");
        last.put("error", error.getMessage() == null ? error.getClass().getName() : error.getMessage());
        last.put("errorType", error.getClass().getName());
        last.put("phase", error.getClass().getName().contains("Conversion") ? "conversion" : last.get("operation"));
      }
    }
    for (Map<String, Object> row : call.exchange.rows) emit(row);
  }

  private JmsOperation active(String operation) {
    for (Call call : calls.get()) {
      if (call.operation.operation.equals(operation)) return call.operation;
      if (call.root) return null;
    }
    return null;
  }

  private JmsOperation current() {
    Call call = calls.get().peek();
    return call == null ? null : call.operation;
  }

  private static Object find(Object[] args, String type) {
    for (Object arg : args) if (JmsAccess.type(arg, type)) return arg;
    return null;
  }

  private static String operation(String owner, String name, Object receiver) {
    if (!jmsSurface(owner, receiver)) return null;
    if (name.equals("onMessage") || name.equals("doExecuteListener")) return "process";
    if (name.startsWith("receive") || name.equals("doReceive") || name.equals("doReceiveSelected")) return "receive";
    if (name.equals("convertAndSend") || name.equals("send") || name.equals("doSend") || name.equals("publish")) {
      return "send";
    }
    return null;
  }

  private static boolean jmsSurface(String owner, Object receiver) {
    return owner.startsWith("org.springframework.jms.") || owner.startsWith("javax.jms.")
        || owner.startsWith("jakarta.jms.") || JmsAccess.type(receiver, "MessageProducer")
        || JmsAccess.type(receiver, "JMSProducer") || JmsAccess.type(receiver, "MessageConsumer")
        || JmsAccess.type(receiver, "JMSConsumer") || JmsAccess.type(receiver, "MessageListener")
        || JmsAccess.type(receiver, "org.springframework.jms.listener.SessionAwareMessageListener")
        || JmsAccess.type(receiver, "org.springframework.jms.core.JmsMessagingTemplate");
  }

  private static boolean isPhase(String name) {
    return name.equals("fromMessage") || name.equals("toMessage") || name.equals("handleListenerException")
        || name.equals("commitIfNecessary") || name.startsWith("rollback");
  }

  private static Phase phase(String name, JmsOperation operation, Object[] args) {
    if (operation == null) return null;
    String stage = name.equals("fromMessage") || name.equals("toMessage") ? "conversion"
        : name.startsWith("commit") ? "commit" : name.startsWith("rollback") ? "rollback" : "handler";
    if (name.equals("handleListenerException") && args.length > 0 && args[0] instanceof Throwable) {
      operation.failed((Throwable) args[0], stage);
    }
    return new Phase(operation, stage);
  }

  public void onDisarm(String kind) {
    if (!kind.equals("jms")) return;
    pending.drain();
    active.clear();
    nativeOperations.clear();
  }

  public List<Map<String, Object>> terminalRows(String kind, Map<String, Object> params) {
    return kind.equals("jms") ? active.terminalRows() : Collections.emptyList();
  }

  public void onOperation(Map<String, Object> metadata) {
    if (!(metadata.get("attributes") instanceof Map)) return;
    Map<?, ?> attributes = (Map<?, ?>) metadata.get("attributes");
    if (!"jms".equals(attributes.get("messaging.system"))) return;
    Object id = metadata.get("operationId");
    if (id == null) throw new IllegalArgumentException("JMS native operation requires operationId");
    JmsOperation operation = current();
    if (operation != null && operation.operation.equals(nativeOperation(attributes))) {
      nativeMetadata(operation, metadata, attributes);
      return;
    }
    if ("start".equals(metadata.get("phase"))) {
      String name = nativeOperation(attributes);
      if (name == null) return;
      operation = new JmsOperation(name);
      if (!reserve(operation)) return;
      operation.put("startedAt", metadata.get("startedAt"));
      nativeMetadata(operation, metadata, attributes);
      synchronized (nativeOperations) {
        if (nativeOperations.size() >= 8192) throw new IllegalStateException("JMS native operation capacity exceeded");
        nativeOperations.put(id, operation);
      }
    } else if ("end".equals(metadata.get("phase"))) {
      operation = nativeOperations.remove(id);
      if (operation == null) return;
      nativeMetadata(operation, metadata, attributes);
      Object error = metadata.get("error");
      Map<String, Object> row = operation.finish(error == null ? null : new IllegalStateException(error.toString()), "success", 0);
      if (row != null) {
        Object duration = metadata.get("durationNs");
        if (duration instanceof Number) row.put("durationMs", ((Number) duration).doubleValue() / 1000000);
        emit(row);
      }
      active.remove(operation);
    }
  }

  private static String nativeOperation(Map<?, ?> attributes) {
    Object type = attributes.get("messaging.operation.type");
    if (type == null) type = attributes.get("messaging.operation");
    if ("publish".equals(type)) return "send";
    return "send".equals(type) || "receive".equals(type) || "process".equals(type) ? type.toString() : null;
  }

  private static void nativeMetadata(JmsOperation operation, Map<String, Object> metadata, Map<?, ?> attributes) {
    operation.put("traceId", metadata.get("traceId"));
    operation.put("spanId", metadata.get("spanId"));
    operation.put("destination", attributes.get("messaging.destination.name"));
    operation.put("messageId", attributes.get("messaging.message.id"));
    operation.put("correlationId", attributes.get("messaging.message.conversation_id"));
    operation.put("subscription", attributes.get("messaging.destination.subscription.name"));
  }

  private static final class Call {
    final JmsOperation operation;
    final boolean root;
    Call(JmsOperation operation, boolean root) {
      this.operation = operation; this.root = root;
    }
  }

  private static final class Exchange {
    final String startedAt = java.time.Instant.now().toString();
    final long startedNanos = System.nanoTime();
    final List<Map<String, Object>> rows = new ArrayList<>();
  }

  private static final class ExchangeCall {
    final Exchange exchange;
    final boolean root;
    ExchangeCall(Exchange exchange, boolean root) { this.exchange = exchange; this.root = root; }
  }

  private static final class Factory {
    final JmsOperation operation;
    Factory(JmsOperation operation) { this.operation = operation; }
  }

  private static final class Phase {
    final JmsOperation operation;
    final String name;
    Phase(JmsOperation operation, String name) { this.operation = operation; this.name = name; }
  }
}
