package com.flanksource.telltale.observer.jms;

import java.lang.reflect.*;
import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.util.*;

final class JmsAccess {
  private JmsAccess() {}

  static boolean type(Object value, String suffix) {
    return value != null && type(value.getClass(), suffix, new HashSet<Class<?>>());
  }

  private static boolean type(Class<?> value, String suffix, Set<Class<?>> seen) {
    if (value == null || !seen.add(value)) return false;
    if (value.getName().equals(suffix) || value.getName().equals("javax.jms." + suffix)
        || value.getName().equals("jakarta.jms." + suffix)) return true;
    for (Class<?> contract : value.getInterfaces()) if (type(contract, suffix, seen)) return true;
    return type(value.getSuperclass(), suffix, seen);
  }

  static Object call(Object receiver, String method) throws Exception {
    if (receiver == null) throw new IllegalArgumentException(method + ": receiver unavailable");
    try {
      return accessibleMethod(receiver.getClass(), method, new HashSet<Class<?>>()).invoke(receiver);
    } catch (InvocationTargetException failure) {
      Throwable cause = failure.getCause();
      if (cause instanceof Exception) throw (Exception) cause;
      throw new IllegalStateException(method + " failed", cause);
    }
  }

  private static Method accessibleMethod(Class<?> type, String name, Set<Class<?>> seen)
      throws NoSuchMethodException {
    if (type == null || !seen.add(type)) throw new NoSuchMethodException(name);
    for (Class<?> contract : type.getInterfaces()) {
      try { return accessibleMethod(contract, name, seen); }
      catch (NoSuchMethodException absent) { /* Continue searching public contracts. */ }
    }
    if (Modifier.isPublic(type.getModifiers())) {
      try { return type.getMethod(name); }
      catch (NoSuchMethodException absent) { /* The superclass may expose the method. */ }
    }
    return accessibleMethod(type.getSuperclass(), name, seen);
  }

  static Object read(Object value, String method, JmsOperation operation) {
    try { return call(value, method); }
    catch (Exception error) {
      operation.metadataError(method + ": " + error.getClass().getSimpleName() + ": " + error.getMessage());
      return null;
    }
  }

  static void destination(Object value, String field, JmsOperation operation) {
    if (value == null) return;
    String getter = type(value, "Queue") ? "getQueueName" : type(value, "Topic") ? "getTopicName" : null;
    if (getter == null) {
      operation.metadataError(field + ": destination implements neither Queue nor Topic");
      return;
    }
    operation.put(field, read(value, getter, operation));
    if (field.equals("destination")) {
      operation.put("destinationType", getter.equals("getQueueName") ? "queue" : "topic");
    }
  }

  static void message(JmsOperation operation, int bodyLimit) {
    Object message = operation.message();
    if (message == null) return;
    operation.put("messageType", message.getClass().getName());
    destination(read(message, "getJMSDestination", operation), "destination", operation);
    destination(read(message, "getJMSReplyTo", operation), "replyTo", operation);
    operation.put("messageId", read(message, "getJMSMessageID", operation));
    operation.put("correlationId", read(message, "getJMSCorrelationID", operation));
    operation.put("redelivered", read(message, "getJMSRedelivered", operation));
    if (bodyLimit <= 0 || operation.bodyInspected) return;
    operation.bodyInspected = true;
    if (!type(message, "TextMessage")) {
      operation.put("bodyUnsupported", "Non-text JMS bodies are not read: " + message.getClass().getName());
      return;
    }
    Object text = read(message, "getText", operation);
    if (text instanceof String) body((String) text, bodyLimit, operation);
  }

  static void body(String text, int maxBytes, JmsOperation operation) {
    byte[] bytes = text.getBytes(StandardCharsets.UTF_8);
    if (bytes.length <= maxBytes) { operation.put("body", text); return; }
    operation.put("body", truncate(text, maxBytes));
    operation.put("bodyTruncated", true);
  }

  static String truncate(String text, int maxBytes) {
    byte[] bytes = text.getBytes(StandardCharsets.UTF_8);
    try {
      return StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.IGNORE)
          .decode(ByteBuffer.wrap(bytes, 0, Math.min(bytes.length, maxBytes))).toString();
    } catch (CharacterCodingException impossible) {
      throw new IllegalStateException("UTF-8 message truncation failed", impossible);
    }
  }
}
