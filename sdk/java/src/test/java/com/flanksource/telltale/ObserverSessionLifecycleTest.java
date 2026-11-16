package com.flanksource.telltale;

import static org.junit.Assert.*;
import com.flanksource.telltale.observer.ObserverRuntime;
import com.google.gson.JsonObject;
import java.io.EOFException;
import java.lang.reflect.*;
import java.net.ServerSocket;
import java.time.Instant;
import java.util.concurrent.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class ObserverSessionLifecycleTest {
  @Test public void releasesObserverHooksWhenTheProtocolCaptureDeadlineExpires() throws Exception {
    exercise(true);
  }

  @Test public void releasesObserverHooksWhenTheAuthenticatedConnectionDisconnects() throws Exception {
    exercise(false);
  }

  private void exercise(boolean deadline) throws Exception {
    java.lang.instrument.Instrumentation instrumentation = ByteBuddyAgent.install();
    com.flanksource.telltale.diag.agent.DiagAgent.agentmain("", instrumentation);
    ObserverRuntime.install(instrumentation);
    JsonObject options = new JsonObject();
    options.addProperty("token", "lifecycle-fixture-token");
    Constructor<Agent> constructor = Agent.class.getDeclaredConstructor(JsonObject.class);
    constructor.setAccessible(true);
    Agent agent = constructor.newInstance(options);
    MemoryWire wire = new MemoryWire();
    ExecutorService worker = Executors.newSingleThreadExecutor();
    Future<Exception> session = worker.submit(() -> {
      try { new Session(agent, wire, false).run(); return null; }
      catch (Exception error) { return error; }
    });
    try {
      JsonObject welcome = new JsonObject();
      welcome.addProperty("token", "lifecycle-fixture-token");
      wire.input.add(frame("welcome", welcome));
      assertTrue(wire.await("auth").getAsJsonObject("body").get("accepted").getAsBoolean());
      wire.await("hello");
      JsonObject arm = new JsonObject();
      arm.addProperty("session", deadline ? "observer-deadline" : "observer-disconnect");
      arm.addProperty("probe", "http");
      arm.add("params", new JsonObject());
      if (deadline) arm.addProperty("stopAt", Instant.now().plusSeconds(1).toString());
      wire.input.add(frame("arm", arm));
      JsonObject armed = wire.await("armed").getAsJsonObject("body");
      assertFalse(armed.toString(), armed.has("error"));
      assertTrue(ObserverRuntime.armed("http"));
      if (deadline) {
        JsonObject ended = wire.await("ended").getAsJsonObject("body");
        assertTrue(ended.toString(), ended.get("complete").getAsBoolean());
        assertFalse(ObserverRuntime.armed("http"));
      }
      wire.disconnect();
      assertTrue(session.get(10, TimeUnit.SECONDS) instanceof EOFException);
      assertFalse(ObserverRuntime.armed("http"));
    } finally {
      wire.disconnect(); worker.shutdownNow();
      Field listener = Agent.class.getDeclaredField("listener"); listener.setAccessible(true);
      ((ServerSocket) listener.get(agent)).close();
      java.lang.management.ManagementFactory.getPlatformMBeanServer()
          .unregisterMBean(new javax.management.ObjectName(RuntimeService.NAME));
    }
  }

  private static JsonObject frame(String type, JsonObject body) {
    JsonObject frame = new JsonObject(); frame.addProperty("t", type); frame.add("body", body); return frame;
  }

  private static final class MemoryWire extends Wire {
    final BlockingQueue<Object> input = new LinkedBlockingQueue<>();
    final BlockingQueue<JsonObject> output = new LinkedBlockingQueue<>();
    private static final Object DISCONNECTED = new Object();
    JsonObject read() throws Exception {
      Object next = input.take();
      if (next == DISCONNECTED) throw new EOFException("fixture connection closed");
      return (JsonObject) next;
    }
    synchronized void send(String type, JsonObject body, byte[] blob) { output.add(frame(type, body.deepCopy())); }
    void disconnect() { input.offer(DISCONNECTED); }
    JsonObject await(String type) throws Exception {
      long until = System.nanoTime() + TimeUnit.SECONDS.toNanos(15);
      while (System.nanoTime() < until) {
        JsonObject next = output.poll(1, TimeUnit.SECONDS);
        if (next != null && type.equals(next.get("t").getAsString())) return next;
      }
      throw new AssertionError("missing protocol frame " + type);
    }
  }
}
