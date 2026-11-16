package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.context.Context;
import io.opentelemetry.instrumentation.api.instrumenter.Instrumenter;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class OtelBoundaryTest {
  @Test public void reportsAnAlreadyStartedOperationWithoutInventingItsStart() throws Exception {
    Instrumenter<String, String> instrumenter = Instrumenter.<String, String>builder(
        OpenTelemetry.noop(), "boundary-fixture", request -> request).buildInstrumenter();
    Context beforeAttachment = instrumenter.start(Context.root(), "already-started");
    ObserverRuntime.install(ByteBuddyAgent.install());
    ObserverRuntime.register(new ObserverAdapter() {
      public String id() { return "boundary-fixture"; }
      public Set<String> kinds() { return Collections.singleton("boundary-fixture"); }
      public boolean matchesType(String name, Set<String> hierarchy) { return false; }
      public boolean matchesMethod(String name, String descriptor) { return false; }
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return null; }
      public Object onExit(Object token, Object result, Throwable error) { return result; }
      public void onOperation(Map<String, Object> metadata) { ObserverRuntime.emit("boundary-fixture", metadata); }
    });
    try (ObserverSubscription capture = ObserverRuntime.subscribe("boundary-fixture", "boundary-fixture", Collections.emptyMap())) {
      instrumenter.end(beforeAttachment, "already-started", "complete", null);
      assertEquals(1L, capture.report().get("unmatchedPreAttachmentEnds"));
      assertTrue(capture.drain().isEmpty());
    }
  }
}
