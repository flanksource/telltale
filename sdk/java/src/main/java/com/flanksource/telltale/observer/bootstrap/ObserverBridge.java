package com.flanksource.telltale.observer.bootstrap;

import java.util.function.BiFunction;

/** The only observer class added to bootstrap; every value crossing this boundary is a JDK type. */
public final class ObserverBridge {
  public static volatile BiFunction<String, Object[], Object> handler;
  private static final ThreadLocal<Boolean> dispatching = new ThreadLocal<>();
  private ObserverBridge() {}

  public static Object enter(String adapter, String owner, String name, String descriptor,
      Object receiver, Object[] args) {
    return invoke("enter", new Object[] {adapter, owner, name, descriptor, receiver, args}, null);
  }

  public static Object exit(Object token, Object result, Throwable error) {
    return token == null ? result : invoke("exit", new Object[] {token, result, error}, result);
  }

  public static void operation(String phase, Object instrumenter, Object context, Object request,
      Object response, Object error, Object attributes, Object name, Object kind, Object time) {
    invoke("operation", new Object[] {phase, instrumenter, context, request, response, error,
        attributes, name, kind, time}, null);
  }

  private static Object invoke(String action, Object[] args, Object unchanged) {
    BiFunction<String, Object[], Object> callback = handler;
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
}
