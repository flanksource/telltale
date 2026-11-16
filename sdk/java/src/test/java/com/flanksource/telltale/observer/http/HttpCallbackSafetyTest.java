package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.lang.reflect.Method;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;
import reactor.core.publisher.Flux;

class HttpCallbackSafetyTest {
  @Test void failedAsyncDecorationInvokesTheOriginalCallbackOnceAndPreservesItsError() {
    AtomicInteger called = new AtomicInteger();
    IllegalArgumentException application = new IllegalArgumentException("application callback failed");
    assertSame(application, assertThrows(IllegalArgumentException.class, () -> HttpCallbacks.assemble(() -> {
      called.incrementAndGet(); throw application;
    }, () -> { throw new IllegalStateException("observer assembly failed"); })));
    assertEquals(1, called.get());
  }

  @Test void observationFailureCannotReplaceBodySignalsOrDemand() throws Exception {
    HttpRegistry registry = new HttpRegistry(() -> true, () -> 32, row -> {});
    HttpOperation operation = registry.create("outgoing", "GET", "https://example.com/body");
    Flux<String> original = Flux.just("application-value");
    Flux<?> observed = body(new ReactiveHttp(registry), original, operation);
    assertEquals(Collections.singletonList("application-value"), observed.collectList().block());
  }

  @Test void completionEncodingFailureCannotReplaceTheOriginalBodyError() throws Exception {
    HttpRegistry registry = new HttpRegistry(() -> true, () -> 32, row -> { throw new IllegalStateException("encoder failed"); });
    HttpOperation operation = registry.create("outgoing", "GET", "https://example.com/body");
    IOException original = new IOException("wire failed");
    Flux<?> observed = body(new ReactiveHttp(registry), Flux.error(original), operation);
    assertSame(original, assertThrows(Exception.class, () -> observed.blockLast()).getCause());
  }

  private static Flux<?> body(ReactiveHttp observer, Flux<?> publisher, HttpOperation operation) throws Exception {
    Method body = ReactiveHttp.class.getDeclaredMethod("body", Object.class, HttpOperation.class, boolean.class, boolean.class);
    body.setAccessible(true);
    return (Flux<?>) body.invoke(observer, publisher, operation, false, true);
  }
}
