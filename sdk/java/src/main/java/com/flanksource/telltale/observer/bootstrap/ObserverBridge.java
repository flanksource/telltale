package com.flanksource.telltale.observer.bootstrap;

import java.util.function.BiFunction;

/** The only observer class added to bootstrap; every value crossing this boundary is a JDK type. */
public final class ObserverBridge {
  public static volatile BiFunction<String, Object[], Object> handler;
  private static final ThreadLocal<Boolean> dispatching = new ThreadLocal<>();
  private ObserverBridge() {}

  public static Object enter(String adapter, String owner, String name, String descriptor,
      Object receiver, Object[] args) {
    BiFunction<String, Object[], Object> callback = handler;
    Object token = invoke(callback, "enter", new Object[] {adapter, owner, name, descriptor, receiver, args}, null);
    return token == null ? null : new Token(callback, token);
  }

  public static Object exit(Object token, Object result, Throwable error) {
    if (token == null) return result;
    if (!(token instanceof Token)) {
      invoke(handler, "failure", new Object[] {new IllegalStateException("invalid observer invocation token")}, null);
      return result;
    }
    Token invocation = (Token) token;
    return invoke(invocation.callback, "exit", new Object[] {invocation.value, result, error}, result);
  }

  public static void operation(String phase, Object instrumenter, Object context, Object request,
      Object response, Object error, Object attributes, Object name, Object kind, Object time) {
    invoke(handler, "operation", new Object[] {phase, instrumenter, context, request, response, error,
        attributes, name, kind, time}, null);
  }

  private static Object invoke(BiFunction<String, Object[], Object> callback, String action, Object[] args, Object unchanged) {
    if (callback == null || Boolean.TRUE.equals(dispatching.get())) return unchanged;
    dispatching.set(true);
    try {
      return callback.apply(action, args);
    } catch (Throwable failure) {
      // Capture failures are delivered to the runtime, never to an application's operation.
      try { callback.apply("failure", new Object[] {failure}); }
      catch (Throwable reporting) { if (reporting != failure) failure.addSuppressed(reporting); }
      return unchanged;
    } finally {
      dispatching.remove();
    }
  }

  private static final class Token {
    final BiFunction<String, Object[], Object> callback;
    final Object value;
    Token(BiFunction<String, Object[], Object> callback, Object value) {
      this.callback = callback; this.value = value;
    }
  }
}
