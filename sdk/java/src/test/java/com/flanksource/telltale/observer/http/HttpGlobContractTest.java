package com.flanksource.telltale.observer.http;

import static org.junit.Assert.*;

import java.util.*;
import org.junit.Test;

public class HttpGlobContractTest {
  @Test public void keepsBracesLiteralAndRepeatedStarsWithinOnePathSegment() {
    assertMatch("/orders/{id}", "/orders/id", false);
    assertMatch("/orders/{id}", "/orders/%7Bid%7D", true);
    assertMatch("/orders/**", "/orders/7/items", false);
    assertMatch("/orders/**", "/orders/7", true);
  }

  @Test public void usesGoCharacterClassesAndEscapes() {
    assertMatch("/orders/[^0-9]", "/orders/a", true);
    assertMatch("/orders/[^0-9]", "/orders/7", false);
    assertMatch("/orders/[!a]", "/orders/a", true);
    assertMatch("/orders/[!a]", "/orders/b", false);
    assertMatch("/orders/\\*", "/orders/*", true);
    assertMatch("/orders/?", "/orders/😀", true);
    assertMatch("/orders/[z-a]", "/orders/a", false);
    assertMatch("/orders/[^z-a]", "/orders/a", true);
  }

  @Test public void rejectsInvalidSelectionPatternsBeforeArming() {
    HttpObserver observer = new HttpObserver(new HttpRegistry(() -> true, () -> 0, row -> {}));
    for (String pattern : new String[] {"", "[", "[]", "[a-]", "trailing\\"}) {
      assertThrows(pattern, IllegalArgumentException.class, () -> observer.validate("http_top", Collections.<String, Object>singletonMap("paths", Collections.singletonList(pattern))));
    }
  }

  private static void assertMatch(String glob, String path, boolean expected) {
    HttpOperation operation = new HttpOperation("outgoing", "GET", "https://example.com" + path, 0, row -> {});
    Map<String, Object> selected = HttpProjection.project("http", Collections.<String, Object>singletonMap("paths", Collections.singletonList(glob)), operation.record());
    assertEquals(glob + " matching " + path, expected, selected != null);
  }
}
