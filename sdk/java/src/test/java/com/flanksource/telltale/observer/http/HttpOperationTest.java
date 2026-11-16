package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

class HttpOperationTest {
  @Test void responseHeadersDoNotFinishAnOpenResponseBody() {
    AtomicInteger completed = new AtomicInteger();
    HttpOperation operation = new HttpOperation("outgoing", "GET", "https://example.com/orders", 4, row -> completed.incrementAndGet());
    operation.response(200, Collections.emptyMap());
    assertEquals(0, completed.get());
    assertEquals("response", ((Map<?, ?>) operation.snapshot().get("attributes")).get("phase"));
    operation.append(false, "abcdef".getBytes(StandardCharsets.UTF_8), 0, 6);
    operation.finish(null);
    operation.finish(new IllegalStateException("late callback"));
    assertEquals(1, completed.get());
    Map<?, ?> content = (Map<?, ?>) ((Map<?, ?>) operation.record().get("response")).get("content");
    assertEquals("abcd", content.get("text"));
    assertEquals(6L, content.get("size"));
    assertEquals(true, content.get("truncated"));
  }

  @Test void redactsCredentialsBeforeProducingARecord() {
    HttpOperation operation = new HttpOperation("incoming", "POST", "https://user:password@example.com/orders?token=private&sort=date", 0, row -> {});
    Map<String, List<String>> headers = new LinkedHashMap<>();
    headers.put("Authorization", Collections.singletonList("Bearer private"));
    headers.put("Content-Type", Collections.singletonList("application/json"));
    operation.requestHeaders(headers);
    Map<?, ?> request = (Map<?, ?>) operation.record().get("request");
    assertEquals("https://example.com/orders?token=%5BREDACTED%5D&sort=date", request.get("url"));
    assertFalse(request.toString().contains("Bearer private"));
    assertFalse(request.toString().contains("password"));
  }

  @Test void exposesTheSameIdentityAndStartTimeAcrossActiveSnapshots() {
    HttpOperation operation = new HttpOperation("incoming", "GET", "https://example.com/orders/7", 0, row -> {});
    operation.attribute("route", "/orders/{id}");
    Map<String, Object> first = operation.snapshot();
    Map<String, Object> second = operation.snapshot();
    assertEquals(first.get("operationId"), second.get("operationId"));
    assertEquals(((Map<?, ?>) first.get("attributes")).get("startedAt"), ((Map<?, ?>) second.get("attributes")).get("startedAt"));
    assertTrue(((Number) second.get("elapsedNs")).longValue() >= ((Number) first.get("elapsedNs")).longValue());
  }

  @Test void preservesBinaryBodyBytesWithoutLossyUtf8Replacement() {
    HttpOperation operation = new HttpOperation("outgoing", "GET", "https://example.com/binary", 16, row -> {});
    byte[] binary = {(byte) 0xff, 0, (byte) 0x80};
    operation.append(false, binary, 0, binary.length);
    Map<String, Object> row = operation.record();
    assertEquals("base64", row.get("encoding"));
    Map<?, ?> content = (Map<?, ?>) ((Map<?, ?>) row.get("response")).get("content");
    assertArrayEquals(binary, Base64.getDecoder().decode(content.get("text").toString()));
  }

  @Test void waitsForTheCurrentReadBeforeHonoringTransportClose() {
    List<Map<String, Object>> rows = new ArrayList<>();
    HttpOperation operation = new HttpOperation("outgoing", "GET", "https://example.com/body", 64, rows::add);
    operation.holdBody();
    assertFalse(operation.finish(null));
    operation.append(false, "body".getBytes(StandardCharsets.UTF_8), 0, 4);
    assertTrue(operation.releaseBody());
    assertTrue(operation.finish(operation.pendingError()));
    assertEquals(1, rows.size());
    assertEquals("body", ((Map<?, ?>) ((Map<?, ?>) rows.get(0).get("response")).get("content")).get("text"));
  }
}
