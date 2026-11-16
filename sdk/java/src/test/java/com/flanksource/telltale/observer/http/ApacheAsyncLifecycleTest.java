package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.apache.hc.client5.http.async.methods.*;
import org.apache.hc.core5.concurrent.FutureCallback;
import org.apache.hc.core5.http.*;
import org.apache.hc.core5.http.message.BasicHttpResponse;
import org.apache.hc.core5.http.nio.AsyncResponseConsumer;
import org.apache.hc.core5.http.impl.BasicEntityDetails;
import org.apache.hc.core5.http.protocol.HttpCoreContext;
import org.junit.jupiter.api.Test;

class ApacheAsyncLifecycleTest {
  @Test void emitsTheCompletedBodyBeforeTheApplicationFutureCallback() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    HttpRegistry registry = new HttpRegistry(() -> true, () -> 64, rows::add);
    registry.push(registry.create("outgoing", "GET", "https://example.com/body"));
    Object[] arguments = {SimpleResponseConsumer.create()};
    try { new ApacheAsyncHttp(registry).enter(arguments); }
    finally { registry.pop(registry.current()); }
    @SuppressWarnings("unchecked") AsyncResponseConsumer<SimpleHttpResponse> consumer = (AsyncResponseConsumer<SimpleHttpResponse>) arguments[0];
    AtomicInteger callbacks = new AtomicInteger();
    byte[] bytes = "apache-body".getBytes(StandardCharsets.UTF_8);
    consumer.consumeResponse(new BasicHttpResponse(200), new BasicEntityDetails(bytes.length, ContentType.TEXT_PLAIN), HttpCoreContext.create(), new FutureCallback<SimpleHttpResponse>() {
      @Override public void completed(SimpleHttpResponse response) {
        assertEquals("apache-body", response.getBodyText()); assertEquals(1, rows.size()); callbacks.incrementAndGet();
      }
      @Override public void failed(Exception failure) { fail(failure); }
      @Override public void cancelled() { fail("unexpected cancellation"); }
    });
    consumer.consume(ByteBuffer.wrap(bytes)); consumer.streamEnd(Collections.emptyList());
    assertEquals(1, callbacks.get()); assertTrue(registry.snapshot().isEmpty());
    assertEquals("apache-body", ((Map<?, ?>) ((Map<?, ?>) rows.get(0).get("response")).get("content")).get("text"));
  }
}
