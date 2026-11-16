package com.flanksource.telltale.observer.http;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.*;

final class HttpProjection {
  private HttpProjection() {}

  static int bodyLimit(Map<String, Object> options) {
    Object configured = options.get("bodies");
    if (configured == null || configured.toString().isEmpty()) return 256 << 10;
    String value = configured.toString().trim();
    long multiplier = 1;
    for (Map.Entry<String, Long> unit : units().entrySet()) if (value.toLowerCase(Locale.ROOT).endsWith(unit.getKey())) {
      value = value.substring(0, value.length() - unit.getKey().length()).trim(); multiplier = unit.getValue(); break;
    }
    long size;
    try { size = Math.multiplyExact(Long.parseLong(value), multiplier); }
    catch (ArithmeticException | NumberFormatException error) { throw new IllegalArgumentException("invalid HTTP bodies size: " + configured, error); }
    if (size < 0 || size > 4 << 20) throw new IllegalArgumentException("HTTP bodies must be between zero and 4MiB");
    return (int) size;
  }

  private static Map<String, Long> units() {
    Map<String, Long> units = new LinkedHashMap<>();
    String[] suffixes = {"k", "m", "g", "t"};
    for (int index = 0; index < suffixes.length; index++) units.put(suffixes[index] + "ib", 1L << (10 * (index + 1)));
    long decimal = 1;
    for (String suffix : suffixes) { decimal *= 1000; units.put(suffix + "b", decimal); }
    for (int index = 0; index < suffixes.length; index++) units.put(suffixes[index] + "i", 1L << (10 * (index + 1)));
    decimal = 1;
    for (String suffix : suffixes) { decimal *= 1000; units.put(suffix, decimal); }
    units.put("b", 1L);
    return units;
  }

  @SuppressWarnings("unchecked")
  static Map<String, Object> project(String kind, Map<String, Object> options, Map<String, Object> original) {
    Map<String, Object> attributes = kind.equals("http_top") ? (Map<String, Object>) original.get("attributes") : original;
    if (kind.equals("http_top") && options.get("minElapsedNs") instanceof Number && ((Number) original.get("elapsedNs")).longValue() < ((Number) options.get("minElapsedNs")).longValue()) return null;
    Object direction = options.get("direction");
    if (direction != null && !direction.equals("both") && !direction.equals("") && !direction.equals(attributes.get("direction"))) return null;
    String url = String.valueOf(attributes.get("url"));
    if (!url.equals("null") && !url.isEmpty()) {
      URI parsed = URI.create(url);
      if (!matches(options.get("hosts"), parsed.getHost()) || !matches(options.get("paths"), parsed.getPath())) return null;
    }
    Map<String, Object> result = (Map<String, Object>) copy(original);
    if (kind.equals("http_top")) return result;
    int cap = bodyLimit(options);
    Set<String> redact = new HashSet<>();
    if (options.get("redact") instanceof Iterable) for (Object header : (Iterable<?>) options.get("redact")) redact.add(header.toString().toLowerCase(Locale.ROOT));
    for (String side : new String[] {"request", "response"}) {
      Map<String, Object> message = (Map<String, Object>) result.get(side);
      if (message == null) continue;
      for (Object value : (List<?>) message.get("headers")) {
        Map<String, Object> header = (Map<String, Object>) value;
        if (redact.contains(header.get("name").toString().toLowerCase(Locale.ROOT))) header.put("value", "[REDACTED]");
      }
      Map<String, Object> body = (Map<String, Object>) message.get(side.equals("request") ? "postData" : "content");
      if (body == null) continue;
      String text = String.valueOf(body.get("text"));
      boolean binary = "base64".equals(body.get("encoding"));
      byte[] bytes = binary ? Base64.getDecoder().decode(text) : text.getBytes(StandardCharsets.UTF_8);
      if (bytes.length > cap) {
        HttpBodies.content(body, Arrays.copyOf(bytes, cap)); body.put("truncated", true);
      }
      String encoding = side.equals("request") ? "requestEncoding" : "encoding";
      if (body.containsKey("encoding")) result.put(encoding, body.get("encoding")); else result.remove(encoding);
    }
    return result;
  }

  private static boolean matches(Object patterns, String value) {
    if (!(patterns instanceof List) || ((List<?>) patterns).isEmpty()) return true;
    for (Object pattern : (List<?>) patterns) if (value != null && HttpGlob.compile(pattern.toString()).matcher(value).matches()) return true;
    return false;
  }

  private static Object copy(Object original) {
    if (original instanceof Map) {
      Map<String, Object> result = new LinkedHashMap<>();
      for (Map.Entry<?, ?> entry : ((Map<?, ?>) original).entrySet()) result.put(entry.getKey().toString(), copy(entry.getValue()));
      return result;
    }
    if (original instanceof List) { List<Object> result = new ArrayList<>(); for (Object value : (List<?>) original) result.add(copy(value)); return result; }
    return original;
  }
}
