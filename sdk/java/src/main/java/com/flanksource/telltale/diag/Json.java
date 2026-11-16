package com.flanksource.telltale.diag;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;
import java.io.IOException;
import java.io.InputStream;

/**
 * Thin Jackson wrapper shared by the diagnostic servlets. Jackson is bundled in this WAR's
 * WEB-INF/lib (this webapp has its own classloader and cannot rely on the host's copy).
 */
public final class Json {

  private static final ObjectMapper MAPPER =
      new ObjectMapper().disable(SerializationFeature.FAIL_ON_EMPTY_BEANS);

  private Json() {}

  public static String write(Object value) {
    try {
      return MAPPER.writeValueAsString(value);
    } catch (IOException e) {
      throw new RuntimeException("serialize JSON: " + e.getMessage(), e);
    }
  }

  public static <T> T read(InputStream in, Class<T> type) throws IOException {
    return MAPPER.readValue(in, type);
  }

  public static <T> T read(InputStream in, TypeReference<T> type) throws IOException {
    return MAPPER.readValue(in, type);
  }
}
