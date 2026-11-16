package com.flanksource.telltale.observer.http;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import java.util.*;
import org.junit.jupiter.api.Test;

class HttpProjectionTest {
  @Test void preservesExactBytesWhenACaptureLimitSplitsUtf8() {
    HttpOperation operation = new HttpOperation("outgoing", "GET", "https://example.com/body", 64, row -> {});
    byte[] bytes = "é".getBytes(StandardCharsets.UTF_8);
    operation.append(false, bytes, 0, bytes.length);
    Map<String, Object> projected = HttpProjection.project("http", Collections.<String, Object>singletonMap("bodies", "1"), operation.record());
    Map<?, ?> content = (Map<?, ?>) ((Map<?, ?>) projected.get("response")).get("content");
    assertEquals("base64", content.get("encoding"));
    assertArrayEquals(new byte[] {bytes[0]}, Base64.getDecoder().decode(content.get("text").toString()));
    assertEquals("base64", projected.get("encoding"));
    assertEquals("é", ((Map<?, ?>) ((Map<?, ?>) operation.record().get("response")).get("content")).get("text"));
  }

  @Test void acceptsTheSameBodySizeSuffixesAsTheGoCaptureOptions() {
    for (String value : new String[] {"1Ki", "1KiB", "1024", "+1 ki"}) assertEquals(1024, HttpProjection.bodyLimit(Collections.<String, Object>singletonMap("bodies", value)));
    for (String value : new String[] {"1K", "1KB"}) assertEquals(1000, HttpProjection.bodyLimit(Collections.<String, Object>singletonMap("bodies", value)));
  }

  @Test void keepsEmojiBodyCapsWithinTheExactByteBudget() {
    byte[] bytes = "😀".getBytes(StandardCharsets.UTF_8);
    HttpOperation operation = new HttpOperation("outgoing", "GET", "https://example.com/body", 64, row -> {});
    operation.append(false, bytes, 0, bytes.length);
    for (int cap = 1; cap <= bytes.length; cap++) {
      Map<String, Object> projected = HttpProjection.project("http", Collections.<String, Object>singletonMap("bodies", Integer.toString(cap)), operation.record());
      Map<?, ?> body = (Map<?, ?>) ((Map<?, ?>) projected.get("response")).get("content");
      byte[] retained = "base64".equals(body.get("encoding")) ? Base64.getDecoder().decode(body.get("text").toString()) : body.get("text").toString().getBytes(StandardCharsets.UTF_8);
      assertArrayEquals(Arrays.copyOf(bytes, cap), retained);
      assertEquals(cap < bytes.length, body.get("truncated"));
    }
  }
}
