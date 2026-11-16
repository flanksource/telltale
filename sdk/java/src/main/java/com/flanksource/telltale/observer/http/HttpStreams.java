package com.flanksource.telltale.observer.http;

import java.nio.charset.StandardCharsets;
import java.nio.ByteBuffer;
import java.util.*;

final class HttpStreams {
  private final HttpRegistry registry;
  private final ThreadLocal<Set<HttpOperation>> inside = ThreadLocal.withInitial(HashSet::new);
  HttpStreams(HttpRegistry registry) { this.registry = registry; }

  Object enter(Object receiver, String name, Object[] args) {
    HttpRegistry.Binding binding = registry.binding(receiver);
    if (binding == null || binding.operation.finished() || !inside.get().add(binding.operation)) return null;
    binding.operation.holdBody();
    return new Token(binding, name, args, receiver instanceof java.io.Reader || receiver instanceof java.io.Writer);
  }

  void exit(Object token, Object result, Throwable error) {
    Token call = (Token) token;
    try {
      if (error != null) { registry.finish(call.binding.operation, error); return; }
      if (call.name.equals("close")) {
        if (!call.binding.request && call.binding.operation.direction.equals("outgoing")) registry.finish(call.binding.operation, null);
        return;
      }
      if (Arrays.asList("string", "bytes", "readUtf8", "readByteArray").contains(call.name)) {
        byte[] bytes = result instanceof byte[] ? (byte[]) result : result.toString().getBytes(StandardCharsets.UTF_8);
        append(call, bytes, 0, bytes.length);
        if (!call.binding.request && call.args.length == 0) registry.finish(call.binding.operation, null);
      } else if (call.name.equals("read")) {
        if (!(result instanceof Number)) return;
        int count = ((Number) result).intValue();
        if (count < 0) { if (!call.binding.request && call.binding.operation.direction.equals("outgoing")) registry.finish(call.binding.operation, null); return; }
        if (call.args.length == 0 && call.characters) characters(call, new char[] {(char) count}, 0, 1);
        else if (call.args.length == 0) append(call, new byte[] {(byte) count}, 0, 1);
        else if (call.args[0] instanceof byte[]) append(call, (byte[]) call.args[0], call.args.length >= 3 ? ((Number) call.args[1]).intValue() : 0, count);
        else if (call.args[0] instanceof char[]) characters(call, (char[]) call.args[0], call.args.length >= 3 ? ((Number) call.args[1]).intValue() : 0, count);
        else if (call.args[0] instanceof ByteBuffer && count > 0) buffer(call, (ByteBuffer) call.args[0], count);
      } else if (call.name.equals("write")) {
        Object value = call.args[0];
        int offset = call.args.length >= 3 ? ((Number) call.args[1]).intValue() : 0;
        if (value instanceof byte[]) append(call, (byte[]) value, offset, call.args.length >= 3 ? ((Number) call.args[2]).intValue() : ((byte[]) value).length);
        else if (value instanceof char[]) characters(call, (char[]) value, offset, call.args.length >= 3 ? ((Number) call.args[2]).intValue() : ((char[]) value).length);
        else if (value instanceof String) {
          String text = (String) value;
          if (call.args.length >= 3) text = text.substring(offset, offset + ((Number) call.args[2]).intValue());
          byte[] bytes = text.getBytes(StandardCharsets.UTF_8); append(call, bytes, 0, bytes.length);
        } else if (value instanceof Number && call.characters) characters(call, new char[] {(char) ((Number) value).intValue()}, 0, 1);
        else if (value instanceof Number) append(call, new byte[] {((Number) value).byteValue()}, 0, 1);
        else if (value instanceof ByteBuffer && result instanceof Number) buffer(call, (ByteBuffer) value, ((Number) result).intValue());
      }
    } finally {
      if (call.binding.operation.releaseBody()) registry.finish(call.binding.operation, call.binding.operation.pendingError());
      inside.get().remove(call.binding.operation);
      if (inside.get().isEmpty()) inside.remove();
    }
  }

  private static void characters(Token token, char[] chars, int offset, int count) {
    byte[] bytes = new String(chars, offset, count).getBytes(StandardCharsets.UTF_8); append(token, bytes, 0, bytes.length);
  }
  private static void append(Token token, byte[] bytes, int offset, int count) { if (token.binding.request || !token.binding.operation.transportBody) token.binding.operation.append(token.binding.request, bytes, offset, count); }
  private static void buffer(Token token, ByteBuffer original, int count) {
    if (count <= 0) return;
    ByteBuffer view = original.asReadOnlyBuffer(); view.position(token.position); view.limit(token.position + count);
    if (token.binding.request || !token.binding.operation.transportBody) token.binding.operation.append(token.binding.request, view);
  }
  private static final class Token {
    final HttpRegistry.Binding binding; final String name; final Object[] args; final boolean characters; final int position;
    Token(HttpRegistry.Binding binding, String name, Object[] args, boolean characters) { this.binding = binding; this.name = name; this.args = args; this.characters = characters; position = args.length > 0 && args[0] instanceof ByteBuffer ? ((ByteBuffer) args[0]).position() : 0; }
  }
}
